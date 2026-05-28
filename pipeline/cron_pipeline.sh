#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# cron_pipeline.sh  — Local pipeline cron job
#
# Runs the full local data-preparation pipeline:
#   1. Download new Faceit CS2 professional demos
#   2. Parse demos with the Go parser (incremental, --no-clear)
#   3. Build / update the per-demo PyG graph cache (.pt files)
#   4. Delete intermediate CSVs to reclaim disk space
#
# The graph cache in gnn/data/cache/ is the artifact sent to the cloud for
# training. Training itself is handled separately by cloud_train.sh.
#
# Crontab example (runs daily at 03:00 local time):
#   0 3 * * * /Users/admin/go-workspace/dem-parser/pipeline/cron_pipeline.sh >> /tmp/cs2_pipeline.log 2>&1
#
# Add to crontab:
#   crontab -e
#   (paste the line above, save)
#
# Required environment variable (add to ~/.zshrc or crontab directly):
#   export FACEIT_API_KEY="your-key-here"
#
# Dependencies:
#   - Go binary built:  cd <repo> && go build -o dem-parser .
#   - Python venv:      cd <repo>/gnn && python3 -m venv venv && pip install -r requirements.txt
#   - Pipeline deps:    source gnn/venv/bin/activate && pip install -r pipeline/requirements.txt
# ─────────────────────────────────────────────────────────────────────────────

set -euo pipefail

# ── Paths ─────────────────────────────────────────────────────────────────────
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PIPELINE_DIR="$REPO_DIR/pipeline"
GNN_DIR="$REPO_DIR/gnn"
INPUT_DIR="$REPO_DIR/input"
OUTPUT_DIR="$REPO_DIR/output"
CACHE_DIR="$GNN_DIR/data/cache"
LOG_DIR="$PIPELINE_DIR/logs"
GO_BINARY="$REPO_DIR/dem-parser"
PYTHON="$GNN_DIR/venv/bin/python3"
STATE_FILE="$PIPELINE_DIR/faceit_downloaded.txt"

# ── Config ────────────────────────────────────────────────────────────────────
MAX_DEMOS="${MAX_DEMOS:-20}"          # demos to download per run (override: MAX_DEMOS=50 ./cron_pipeline.sh)
RETRAIN_THRESHOLD="${RETRAIN_THRESHOLD:-20}"  # trigger cloud_train.sh when cache grows past this
DELETE_CSVS="${DELETE_CSVS:-true}"    # set to "false" to keep CSVs for debugging

# ── Colour helpers ────────────────────────────────────────────────────────────
ts()  { date "+%Y-%m-%d %H:%M:%S"; }
log() { echo "[$(ts)] $*"; }
ok()  { echo "[$(ts)] ✓ $*"; }
err() { echo "[$(ts)] ✗ $*" >&2; }

# ── Pre-flight checks ─────────────────────────────────────────────────────────
log "=== CS2 Tactic Pipeline — Starting ==="
log "Repo: $REPO_DIR"

if [[ -z "${FACEIT_API_KEY:-}" ]]; then
    err "FACEIT_API_KEY is not set. Export it or add to ~/.zshrc:"
    err "  export FACEIT_API_KEY=\"your-key-here\""
    exit 1
fi

if [[ ! -f "$GO_BINARY" ]]; then
    log "Go binary not found — building ..."
    (cd "$REPO_DIR" && go build -o dem-parser .)
    ok "Go binary built"
fi

if [[ ! -f "$PYTHON" ]]; then
    err "Python venv not found at $PYTHON"
    err "Run: cd $GNN_DIR && python3 -m venv venv && pip install -r requirements.txt"
    exit 1
fi

mkdir -p "$INPUT_DIR" "$OUTPUT_DIR" "$CACHE_DIR" "$LOG_DIR"

RUN_LOG="$LOG_DIR/run_$(date +%Y%m%d_%H%M%S).log"
log "Run log: $RUN_LOG"
exec > >(tee -a "$RUN_LOG") 2>&1   # tee all output to log file from here

# ── Stage 1: Download ─────────────────────────────────────────────────────────
log "--- Stage 1: Downloading up to $MAX_DEMOS new demos ---"

DEMOS_BEFORE=$(find "$INPUT_DIR" -name "*.dem" 2>/dev/null | wc -l | tr -d ' ')

"$PYTHON" "$PIPELINE_DIR/faceit_download.py" \
    --limit "$MAX_DEMOS" \
    --output "$INPUT_DIR" \
    --state  "$STATE_FILE" \
    --api-key "$FACEIT_API_KEY"

DEMOS_AFTER=$(find "$INPUT_DIR" -name "*.dem" 2>/dev/null | wc -l | tr -d ' ')
NEW_DEMOS=$(( DEMOS_AFTER - DEMOS_BEFORE ))
ok "Stage 1 done: $NEW_DEMOS new demo(s) downloaded ($DEMOS_AFTER total in input/)"

if [[ "$NEW_DEMOS" -eq 0 ]]; then
    log "No new demos — nothing more to do."
    log "=== Pipeline done (no new data) ==="
    exit 0
fi

# ── Stage 2: Parse ────────────────────────────────────────────────────────────
log "--- Stage 2: Parsing demos with Go parser (--no-clear) ---"

CSV_BEFORE=$(find "$OUTPUT_DIR" -name "*_map_activities.csv" 2>/dev/null | wc -l | tr -d ' ')

"$GO_BINARY" \
    --input  "$INPUT_DIR" \
    --output "$OUTPUT_DIR" \
    --no-clear

CSV_AFTER=$(find "$OUTPUT_DIR" -name "*_map_activities.csv" 2>/dev/null | wc -l | tr -d ' ')
NEW_CSV=$(( CSV_AFTER - CSV_BEFORE ))
ok "Stage 2 done: $NEW_CSV new demo(s) parsed ($CSV_AFTER total CSVs)"

# ── Stage 3: Build graph cache ────────────────────────────────────────────────
log "--- Stage 3: Building per-demo graph cache ---"

PT_BEFORE=$(find "$CACHE_DIR" -name "*.pt" 2>/dev/null | wc -l | tr -d ' ')

"$PYTHON" "$GNN_DIR/build_graphs.py" \
    "$OUTPUT_DIR" \
    --incremental \
    --cache-dir "$CACHE_DIR"

PT_AFTER=$(find "$CACHE_DIR" -name "*.pt" 2>/dev/null | wc -l | tr -d ' ')
NEW_PT=$(( PT_AFTER - PT_BEFORE ))
ok "Stage 3 done: $NEW_PT new graph file(s) cached ($PT_AFTER total in cache/)"

# ── Stage 4: Delete CSVs ──────────────────────────────────────────────────────
if [[ "$DELETE_CSVS" == "true" ]]; then
    log "--- Stage 4: Cleaning up intermediate CSVs ---"
    DELETED=0
    for f in "$OUTPUT_DIR"/*.csv; do
        [[ -f "$f" ]] && rm "$f" && (( DELETED++ )) || true
    done
    ok "Stage 4 done: deleted $DELETED CSV file(s)"
else
    log "--- Stage 4: Skipped (DELETE_CSVS=false) ---"
fi

# ── Stage 5: Trigger cloud training? ─────────────────────────────────────────
TOTAL_PT=$(find "$CACHE_DIR" -name "*.pt" 2>/dev/null | wc -l | tr -d ' ')

if [[ "$TOTAL_PT" -ge "$RETRAIN_THRESHOLD" ]]; then
    log "--- Stage 5: Cache has $TOTAL_PT demos (≥ $RETRAIN_THRESHOLD) → launching cloud training ---"
    if [[ -f "$PIPELINE_DIR/cloud_train.sh" ]]; then
        bash "$PIPELINE_DIR/cloud_train.sh" &
        ok "cloud_train.sh launched in background (PID $!)"
    else
        log "cloud_train.sh not found — skipping auto-training."
        log "Run manually: bash $PIPELINE_DIR/cloud_train.sh"
    fi
else
    log "Cache has $TOTAL_PT demos (threshold $RETRAIN_THRESHOLD) — skipping training this run."
fi

log "=== Pipeline done ==="
log "Graph cache: $CACHE_DIR ($TOTAL_PT files)"
log "To train now: bash $PIPELINE_DIR/cloud_train.sh"
