#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# cloud_train.sh — Cheapest GPU training via Vast.ai
#
# Workflow:
#   1. Package the local graph cache (~2.5 GB) into a tarball
#   2. Find the cheapest available RTX 4090 / RTX 3090 on Vast.ai
#   3. Spin up an instance, wait for it to be ready
#   4. Upload the graph cache tarball via SCP
#   5. Run train.py on the instance
#   6. Download model checkpoints back to gnn/data/models/
#   7. Destroy the instance (stop billing)
#
# Requirements:
#   pip install vastai          # Vast.ai CLI
#   vastai set api-key YOUR_KEY  # get key at https://vast.ai/console/account
#
# Override defaults with environment variables:
#   VAST_GPU="RTX_3090"         # cheaper if RTX 4090 unavailable
#   VAST_MAX_PRICE="0.35"       # max $/hr
#   TRAIN_EPOCHS="500"
#   TRAIN_MODE="--full"         # or "" for eval mode (80/20 split)
# ─────────────────────────────────────────────────────────────────────────────

set -euo pipefail

# ── Config ────────────────────────────────────────────────────────────────────
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GNN_DIR="$REPO_DIR/gnn"
CACHE_DIR="$GNN_DIR/data/cache"
MODELS_DIR="$GNN_DIR/data/models"
PIPELINE_DIR="$REPO_DIR/pipeline"

GPU_NAME="${VAST_GPU:-RTX_4090}"
MAX_PRICE="${VAST_MAX_PRICE:-0.40}"     # $/hr ceiling
MIN_DISK="${VAST_MIN_DISK:-30}"         # GB NVMe on instance
TRAIN_EPOCHS="${TRAIN_EPOCHS:-500}"
TRAIN_MODE="${TRAIN_MODE:-}"            # "" = per-map eval; "--full" = 100% target

INSTANCE_ID_FILE="$PIPELINE_DIR/.vast_instance_id"
LOG_DIR="$PIPELINE_DIR/logs"
mkdir -p "$MODELS_DIR" "$LOG_DIR"

ts()  { date "+%Y-%m-%d %H:%M:%S"; }
log() { echo "[$(ts)] $*"; }
ok()  { echo "[$(ts)] ✓ $*"; }
err() { echo "[$(ts)] ✗ $*" >&2; }
die() { err "$*"; exit 1; }

# ── Pre-flight ────────────────────────────────────────────────────────────────
log "=== Vast.ai Cloud Training — Starting ==="

command -v vastai >/dev/null 2>&1 || die "vastai CLI not installed. Run: pip install vastai"
command -v scp    >/dev/null 2>&1 || die "scp not found (install OpenSSH)"

PT_COUNT=$(find "$CACHE_DIR" -name "*.pt" 2>/dev/null | wc -l | tr -d ' ')
[[ "$PT_COUNT" -eq 0 ]] && die "No .pt files in $CACHE_DIR. Run cron_pipeline.sh first."
log "Graph cache: $PT_COUNT demo(s) in $CACHE_DIR"

# ── Stage 1: Package cache ────────────────────────────────────────────────────
log "--- Stage 1: Packaging graph cache ---"
TARBALL="/tmp/cs2_graph_cache_$(date +%Y%m%d_%H%M%S).tar.gz"
tar -czf "$TARBALL" -C "$(dirname "$CACHE_DIR")" "$(basename "$CACHE_DIR")"
SIZE_MB=$(du -m "$TARBALL" | cut -f1)
ok "Tarball: $TARBALL (${SIZE_MB} MB)"

# ── Stage 2: Find cheapest instance ───────────────────────────────────────────
log "--- Stage 2: Finding cheapest $GPU_NAME under \$$MAX_PRICE/hr ---"

SEARCH_RESULT=$(vastai search offers \
    "gpu_name=$GPU_NAME num_gpus=1 disk_space>=$MIN_DISK dph_total<$MAX_PRICE" \
    --order dph_total \
    --limit 3 \
    --raw 2>/dev/null || true)

if [[ -z "$SEARCH_RESULT" ]] || echo "$SEARCH_RESULT" | grep -q '"items": \[\]'; then
    log "No RTX 4090 found under \$$MAX_PRICE/hr — trying RTX 3090 ..."
    SEARCH_RESULT=$(vastai search offers \
        "gpu_name=RTX_3090 num_gpus=1 disk_space>=$MIN_DISK dph_total<0.30" \
        --order dph_total --limit 3 --raw 2>/dev/null || true)
fi

OFFER_ID=$(echo "$SEARCH_RESULT" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    items = data if isinstance(data, list) else data.get('offers', data.get('items', []))
    if items:
        print(items[0]['id'])
except Exception as e:
    pass
" 2>/dev/null || true)

[[ -z "$OFFER_ID" ]] && die "No suitable GPU instance found. Check vastai search offers manually."
log "Selected offer ID: $OFFER_ID"

# ── Stage 3: Create instance ───────────────────────────────────────────────────
log "--- Stage 3: Creating Vast.ai instance ---"

INSTANCE_JSON=$(vastai create instance "$OFFER_ID" \
    --image "pytorch/pytorch:2.4.0-cuda12.4-cudnn9-runtime" \
    --disk "$MIN_DISK" \
    --raw \
    --onstart-cmd "pip install -q torch-geometric torch-scatter torch-sparse scikit-learn")

INSTANCE_ID=$(echo "$INSTANCE_JSON" | python3 -c "
import sys, json
data = json.load(sys.stdin)
print(data.get('new_contract', data.get('id', '')))
" 2>/dev/null || true)

[[ -z "$INSTANCE_ID" ]] && die "Failed to create instance. Response: $INSTANCE_JSON"
echo "$INSTANCE_ID" > "$INSTANCE_ID_FILE"
ok "Instance created: $INSTANCE_ID"

# Cleanup trap — always destroy instance on exit
cleanup() {
    local exit_code=$?
    if [[ -f "$INSTANCE_ID_FILE" ]]; then
        local iid; iid=$(cat "$INSTANCE_ID_FILE")
        log "Destroying instance $iid ..."
        vastai destroy instance "$iid" 2>/dev/null || true
        rm -f "$INSTANCE_ID_FILE"
        ok "Instance destroyed"
    fi
    rm -f "$TARBALL"
    exit $exit_code
}
trap cleanup EXIT INT TERM

# ── Stage 4: Wait for instance to be ready ────────────────────────────────────
log "--- Stage 4: Waiting for instance to start (SSH ready) ---"
MAX_WAIT=300   # 5 minutes
WAITED=0
SSH_PORT=""
SSH_HOST=""

while [[ $WAITED -lt $MAX_WAIT ]]; do
    INSTANCE_INFO=$(vastai show instance "$INSTANCE_ID" --raw 2>/dev/null || true)
    STATUS=$(echo "$INSTANCE_INFO" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    print(d.get('actual_status', 'unknown'))
except: print('unknown')
" 2>/dev/null || echo "unknown")

    if [[ "$STATUS" == "running" ]]; then
        SSH_PORT=$(echo "$INSTANCE_INFO" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    print(d.get('ports', {}).get('22/tcp', [{}])[0].get('HostPort', ''))
except: print('')
" 2>/dev/null || true)
        SSH_HOST=$(echo "$INSTANCE_INFO" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    print(d.get('public_ipaddr', ''))
except: print('')
" 2>/dev/null || true)
        [[ -n "$SSH_PORT" && -n "$SSH_HOST" ]] && break
    fi

    log "  Status: $STATUS — waiting ... (${WAITED}s)"
    sleep 15
    WAITED=$(( WAITED + 15 ))
done

[[ -z "$SSH_HOST" || -z "$SSH_PORT" ]] && die "Instance did not become ready in ${MAX_WAIT}s"
ok "Instance ready: $SSH_HOST:$SSH_PORT"

SSH_OPTS="-o StrictHostKeyChecking=no -o ConnectTimeout=30 -p $SSH_PORT"

# Wait for SSH to accept connections
for attempt in 1 2 3 4 5; do
    ssh $SSH_OPTS root@"$SSH_HOST" "echo ok" >/dev/null 2>&1 && break
    log "  SSH attempt $attempt/5 ..."
    sleep 10
done

# ── Stage 5: Upload cache ──────────────────────────────────────────────────────
log "--- Stage 5: Uploading graph cache (${SIZE_MB} MB) ---"
scp $SSH_OPTS "$TARBALL" root@"$SSH_HOST":~/graph_cache.tar.gz
ok "Upload done"

# ── Stage 6: Train ────────────────────────────────────────────────────────────
log "--- Stage 6: Training on instance ($GPU_NAME) ---"

ssh $SSH_OPTS root@"$SSH_HOST" bash -s <<REMOTE
set -e
echo "[remote] Extracting graph cache ..."
mkdir -p dem-parser/gnn/data
tar -xzf graph_cache.tar.gz -C dem-parser/gnn/data/

echo "[remote] Cloning repo for Python files ..."
# Copy only the Python training scripts (no large files)
mkdir -p dem-parser/gnn

echo "[remote] Installing Python deps ..."
pip install -q torch-geometric scikit-learn tqdm 2>&1 | tail -5

echo "[remote] Starting training ..."
cd dem-parser/gnn
REMOTE
# Upload Python scripts
scp $SSH_OPTS \
    "$GNN_DIR/train.py" \
    "$GNN_DIR/build_graphs.py" \
    "$GNN_DIR/model.py" \
    root@"$SSH_HOST":~/dem-parser/gnn/

ssh $SSH_OPTS root@"$SSH_HOST" bash -s <<REMOTE
set -e
cd ~/dem-parser/gnn
echo "[remote] Training (epochs=$TRAIN_EPOCHS, mode=${TRAIN_MODE:-eval}) ..."
python3 train.py \
    --graphs-dir data/cache \
    --epochs $TRAIN_EPOCHS \
    $TRAIN_MODE \
    2>&1 | tee /tmp/train_output.log
echo "[remote] Training done."
ls -lh data/best_model_*.pt 2>/dev/null || ls -lh data/full_model_*.pt 2>/dev/null || true
REMOTE

ok "Training complete"

# ── Stage 7: Download models ───────────────────────────────────────────────────
log "--- Stage 7: Downloading model checkpoints ---"
VERSION=$(date +%Y%m%d_%H%M%S)
mkdir -p "$MODELS_DIR"

# Download all best_model_*.pt and full_model_*.pt
scp $SSH_OPTS \
    "root@${SSH_HOST}:~/dem-parser/gnn/data/*_model_*.pt" \
    "$MODELS_DIR/" 2>/dev/null || true

# Also grab the training log
scp $SSH_OPTS \
    "root@${SSH_HOST}:/tmp/train_output.log" \
    "$LOG_DIR/train_${VERSION}.log" 2>/dev/null || true

# Rename downloaded models with version stamp
for f in "$MODELS_DIR"/*_model_*.pt; do
    [[ -f "$f" ]] || continue
    base=$(basename "$f" .pt)
    mv "$f" "$MODELS_DIR/${base}_v${VERSION}.pt"
done

ok "Models saved to $MODELS_DIR"
ls -lh "$MODELS_DIR"/ | grep "$VERSION" || true
log "Training log: $LOG_DIR/train_${VERSION}.log"

# Instance is destroyed by the trap on EXIT
log "=== Cloud Training Done ==="
log "Instance will be destroyed automatically."
