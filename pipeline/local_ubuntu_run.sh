#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# local_ubuntu_run.sh — Daily pipeline runner
#
# Optimised for:
#   GPU  : RTX 3060 12 GB VRAM  → CUDA training, batch_size=32
#   RAM  : 30 GB                → Go workers capped at 12
#   Disk : 500 GB               → batch processing, delete .dem + CSVs after caching
#
# Workflow per run:
#   1. Download up to $BATCH_SIZE new Faceit demos
#   2. Parse each demo with the Go parser (--no-clear, incremental)
#   3. Cache each demo as a .pt graph file
#   4. Delete the .dem file and output CSVs (reclaim ~760 MB/demo)
#   5. If total cached demos ≥ $RETRAIN_THRESHOLD → train locally on RTX 3060
#   6. Save versioned model checkpoints to gnn/data/models/
#
# Designed to be called from cron or run manually:
#   bash pipeline/local_ubuntu_run.sh
#
# Environment variables (override via .env file or export before calling):
#   FACEIT_API_KEY      Required. Get from https://developers.faceit.com/apps
#   BATCH_SIZE          Demos per run (default 40 — keeps peak disk ≤ 30 GB)
#   RETRAIN_THRESHOLD   Retrain trigger (default 50 cached demos)
#   TRAIN_EPOCHS        Epochs for eval-mode training (default 500)
#   TRAIN_MODE          "" = per-map eval, "--full" = 100% training
#   DELETE_DEMOS        Delete .dem after graph build (default true)
#   DELETE_CSVS         Delete CSVs after graph build (default true)
# ─────────────────────────────────────────────────────────────────────────────

set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PIPELINE_DIR="$REPO_DIR/pipeline"
GNN_DIR="$REPO_DIR/gnn"
INPUT_DIR="$REPO_DIR/input"
OUTPUT_DIR="$REPO_DIR/output"
CACHE_DIR="$GNN_DIR/data/cache"
MODELS_DIR="$GNN_DIR/data/models"
LOG_DIR="$PIPELINE_DIR/logs"
GO_BINARY="$REPO_DIR/dem-parser"
PYTHON="$GNN_DIR/venv/bin/python3"
STATE_FILE="$PIPELINE_DIR/faceit_downloaded.txt"

# ── Load .env if present ──────────────────────────────────────────────────────
ENV_FILE="$REPO_DIR/.env"
[[ -f "$ENV_FILE" ]] && source "$ENV_FILE"

# ── Tunables ──────────────────────────────────────────────────────────────────
BATCH_SIZE="${BATCH_SIZE:-40}"            # demos per run  (40 × ~760 MB = 30 GB peak)
RETRAIN_THRESHOLD="${RETRAIN_THRESHOLD:-50}"
TRAIN_EPOCHS="${TRAIN_EPOCHS:-500}"
TRAIN_MODE="${TRAIN_MODE:-}"              # "" = eval (80/20 split); "--full" = 100%
DELETE_DEMOS="${DELETE_DEMOS:-true}"      # free ~500 MB/demo after parsing
DELETE_CSVS="${DELETE_CSVS:-true}"        # free ~260 MB/demo after caching

# Go parser workers: leave 2 logical CPUs for OS (RTX 3060 box is typically 12-core)
CPU_COUNT=$(nproc 2>/dev/null || echo 8)
GO_WORKERS=$(( CPU_COUNT > 2 ? CPU_COUNT - 2 : 1 ))

# ── Helpers ───────────────────────────────────────────────────────────────────
ts()    { date "+%Y-%m-%d %H:%M:%S"; }
log()   { echo "[$(ts)] $*"; }
ok()    { echo "[$(ts)] ✓ $*"; }
warn()  { echo "[$(ts)] ⚠ $*"; }
err()   { echo "[$(ts)] ✗ $*" >&2; exit 1; }
disk_free_gb() { df -BG "$REPO_DIR" | awk 'NR==2 {gsub("G","",$4); print $4}'; }

# ── Preflight ─────────────────────────────────────────────────────────────────
mkdir -p "$INPUT_DIR" "$OUTPUT_DIR" "$CACHE_DIR" "$MODELS_DIR" "$LOG_DIR"

RUN_ID=$(date +%Y%m%d_%H%M%S)
RUN_LOG="$LOG_DIR/run_${RUN_ID}.log"
exec > >(tee -a "$RUN_LOG") 2>&1

log "=== CS2 Local Pipeline — RTX 3060 — Run $RUN_ID ==="
log "Repo:  $REPO_DIR"
log "Batch: $BATCH_SIZE demos | Delete demos: $DELETE_DEMOS | Delete CSVs: $DELETE_CSVS"
log "Disk free: $(disk_free_gb) GB"

[[ -z "${FACEIT_API_KEY:-}" ]] && err "FACEIT_API_KEY is not set. Add it to $ENV_FILE or export it."
[[ -f "$GO_BINARY" ]] || err "Go binary missing. Run: cd $REPO_DIR && go build -o dem-parser ."
[[ -f "$PYTHON" ]]    || err "Python venv missing. Run: bash $PIPELINE_DIR/ubuntu_setup.sh"

# Check CUDA is visible (training will fall back to CPU if not, but warn)
"$PYTHON" -c "import torch; print('[cuda]', torch.cuda.get_device_name(0))" 2>/dev/null \
    && true || warn "CUDA not visible to PyTorch — training will use CPU (slower)"

# Disk safety: abort if less than 35 GB free (one batch needs ~30 GB scratch)
FREE_GB=$(disk_free_gb)
if [[ "$FREE_GB" -lt 35 ]]; then
    err "Only ${FREE_GB} GB free — need ≥35 GB for a batch of $BATCH_SIZE demos. Free up space first."
fi

# ── Stage 1: Download ─────────────────────────────────────────────────────────
log "--- Stage 1: Faceit download (limit=$BATCH_SIZE) ---"

DEMOS_BEFORE=$(find "$INPUT_DIR" -name "*.dem" 2>/dev/null | wc -l | tr -d ' ')

"$PYTHON" "$PIPELINE_DIR/faceit_download.py" \
    --limit   "$BATCH_SIZE" \
    --output  "$INPUT_DIR" \
    --state   "$STATE_FILE" \
    --api-key "$FACEIT_API_KEY"

DEMOS_AFTER=$(find "$INPUT_DIR" -name "*.dem" 2>/dev/null | wc -l | tr -d ' ')
NEW_DEMOS=$(( DEMOS_AFTER - DEMOS_BEFORE ))
ok "Stage 1: $NEW_DEMOS new demo(s) downloaded (total in input/: $DEMOS_AFTER)"
log "Disk free after download: $(disk_free_gb) GB"

if [[ "$NEW_DEMOS" -eq 0 ]]; then
    log "No new demos this run — exiting."
    log "=== Pipeline done (no new data) ==="
    exit 0
fi

# ── Stages 2–4: Per-demo loop (parse → cache → delete) ───────────────────────
# Process each new .dem individually so we can delete it immediately after
# caching its graphs. This keeps peak disk usage at O(1 demo) not O(batch).
log "--- Stages 2–4: Parse → Cache → Delete (per-demo loop) ---"

PROCESSED=0
SKIPPED=0
DEM_FILES=("$INPUT_DIR"/*.dem)

for DEM_FILE in "${DEM_FILES[@]}"; do
    [[ -f "$DEM_FILE" ]] || continue

    MATCH_ID=$(basename "$DEM_FILE" .dem)
    PT_FILE="$CACHE_DIR/${MATCH_ID}.pt"

    # Skip if already cached (idempotent)
    if [[ -f "$PT_FILE" ]]; then
        (( SKIPPED++ )) || true
        # Demo already cached — safe to delete the raw .dem to save space
        if [[ "$DELETE_DEMOS" == "true" ]]; then
            rm -f "$DEM_FILE"
        fi
        continue
    fi

    log "Processing: $MATCH_ID"

    # ── 2. Parse single demo ─────────────────────────────────────────────────
    CSV_BEFORE=$(find "$OUTPUT_DIR" -name "*_map_activities.csv" 2>/dev/null | wc -l | tr -d ' ')

    "$GO_BINARY" \
        --input  "$INPUT_DIR" \
        --output "$OUTPUT_DIR" \
        --no-clear \
        2>&1 | grep -E "(Processing|Completed|Error|map:|round)" | tail -10 || true

    CSV_AFTER=$(find "$OUTPUT_DIR" -name "*_map_activities.csv" 2>/dev/null | wc -l | tr -d ' ')
    NEW_CSV=$(( CSV_AFTER - CSV_BEFORE ))

    if [[ "$NEW_CSV" -eq 0 ]]; then
        warn "  No new CSV produced for $MATCH_ID — demo may be invalid"
        [[ "$DELETE_DEMOS" == "true" ]] && rm -f "$DEM_FILE"
        (( SKIPPED++ )) || true
        continue
    fi

    # ── 3. Build graph cache for this demo ───────────────────────────────────
    ACT_FILE=$(find "$OUTPUT_DIR" -name "${MATCH_ID}*_map_activities.csv" 2>/dev/null | head -1 || true)
    if [[ -z "$ACT_FILE" ]]; then
        # Fallback: find any new CSV produced in this pass
        ACT_FILE=$(find "$OUTPUT_DIR" -name "*_map_activities.csv" \
            -newer "$PT_FILE" 2>/dev/null | head -1 || true)
    fi

    "$PYTHON" "$GNN_DIR/build_graphs.py" \
        "$OUTPUT_DIR" \
        --incremental \
        --cache-dir "$CACHE_DIR" \
        2>&1 | tail -5

    # ── 4a. Delete demo to reclaim ~500 MB ───────────────────────────────────
    if [[ "$DELETE_DEMOS" == "true" ]]; then
        rm -f "$DEM_FILE"
        log "  Deleted demo: $(basename "$DEM_FILE")"
    fi

    # ── 4b. Delete CSVs for this match to reclaim ~260 MB ────────────────────
    if [[ "$DELETE_CSVS" == "true" ]]; then
        # Find and delete all CSVs whose base name matches this match
        find "$OUTPUT_DIR" -name "${MATCH_ID}*" -name "*.csv" -delete 2>/dev/null || true
        # Also delete any other CSVs that now have a corresponding .pt in cache
        while IFS= read -r pt; do
            pt_base=$(basename "$pt" .pt)
            find "$OUTPUT_DIR" -name "${pt_base}*" -name "*.csv" -delete 2>/dev/null || true
        done < <(find "$CACHE_DIR" -name "*.pt" 2>/dev/null)
        log "  Deleted CSVs for: $MATCH_ID"
    fi

    (( PROCESSED++ )) || true
    PT_TOTAL=$(find "$CACHE_DIR" -name "*.pt" 2>/dev/null | wc -l | tr -d ' ')
    log "  [${PROCESSED}/${NEW_DEMOS}] cached | disk free: $(disk_free_gb) GB | cache total: $PT_TOTAL"
done

ok "Stage 2–4: $PROCESSED demo(s) processed, $SKIPPED skipped"
log "Disk free after cleanup: $(disk_free_gb) GB"

# ── Stage 5: Train on RTX 3060 ────────────────────────────────────────────────
PT_TOTAL=$(find "$CACHE_DIR" -name "*.pt" 2>/dev/null | wc -l | tr -d ' ')
log "Cache total: $PT_TOTAL demo(s)"

if [[ "$PT_TOTAL" -ge "$RETRAIN_THRESHOLD" ]]; then
    log "--- Stage 5: Training on RTX 3060 (CUDA) ---"
    log "Mode: ${TRAIN_MODE:-eval (80/20 split)} | Epochs: $TRAIN_EPOCHS | Cache: $PT_TOTAL demos"

    GPU_NAME=$("$PYTHON" -c "import torch; print(torch.cuda.get_device_name(0))" 2>/dev/null || echo "CPU")
    log "Training device: $GPU_NAME"

    TRAIN_CMD=(
        "$PYTHON" "$GNN_DIR/train.py"
        "--graphs-dir" "$CACHE_DIR"
        "--epochs"     "$TRAIN_EPOCHS"
    )
    [[ -n "$TRAIN_MODE" ]] && TRAIN_CMD+=("$TRAIN_MODE")

    TRAIN_LOG="$LOG_DIR/train_${RUN_ID}.log"
    log "Training log: $TRAIN_LOG"

    "${TRAIN_CMD[@]}" 2>&1 | tee "$TRAIN_LOG"

    # ── Stage 6: Version model checkpoints ───────────────────────────────────
    log "--- Stage 6: Archiving model checkpoints ---"
    for MODEL in "$GNN_DIR/data"/best_model_*.pt "$GNN_DIR/data"/full_model_*.pt; do
        [[ -f "$MODEL" ]] || continue
        BASE=$(basename "$MODEL" .pt)
        DEST="$MODELS_DIR/${BASE}_v${RUN_ID}.pt"
        cp "$MODEL" "$DEST"
        ok "  Saved: $(basename "$DEST")"
    done

    # Quick accuracy summary from training log
    log "--- Accuracy summary ---"
    grep -E "(Best test accuracy|Best training accuracy|Average)" "$TRAIN_LOG" 2>/dev/null || true

else
    log "--- Stage 5: Skipped (cache=$PT_TOTAL, threshold=$RETRAIN_THRESHOLD) ---"
    log "Run $BATCH_SIZE more demos to trigger training, or lower RETRAIN_THRESHOLD."
fi

# ── Final report ──────────────────────────────────────────────────────────────
PT_TOTAL=$(find "$CACHE_DIR" -name "*.pt" 2>/dev/null | wc -l | tr -d ' ')
log "=== Run $RUN_ID complete ==="
log "Demos processed this run : $PROCESSED"
log "Graph cache total        : $PT_TOTAL .pt files"
log "Disk free                : $(disk_free_gb) GB"
log "Models dir               : $MODELS_DIR"
log "Log                      : $RUN_LOG"
