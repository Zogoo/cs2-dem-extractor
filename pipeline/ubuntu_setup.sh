#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# ubuntu_setup.sh — One-time machine setup
#
# Target hardware:
#   GPU  : NVIDIA RTX 3060 12 GB VRAM (Ampere, CUDA 12.x)
#   RAM  : 30 GB
#   Disk : 500 GB
#   OS   : Ubuntu 22.04 LTS
#
# Run once after cloning the repo:
#   bash pipeline/ubuntu_setup.sh
#
# After this script completes, use local_ubuntu_run.sh for daily runs.
# ─────────────────────────────────────────────────────────────────────────────

set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GNN_DIR="$REPO_DIR/gnn"
GO_VERSION="1.22.4"
PYTHON_MIN="3.10"

ts()  { date "+%Y-%m-%d %H:%M:%S"; }
log() { echo "[$(ts)] $*"; }
ok()  { echo "[$(ts)] ✓ $*"; }
err() { echo "[$(ts)] ✗ $*" >&2; exit 1; }
need_root() { [[ $EUID -eq 0 ]] || err "Re-run with sudo: sudo bash $0"; }

log "=== Ubuntu Setup — RTX 3060 / 500 GB ==="
log "Repo: $REPO_DIR"

# ── 0. Verify OS ──────────────────────────────────────────────────────────────
if ! grep -qi "ubuntu" /etc/os-release 2>/dev/null; then
    log "Warning: not Ubuntu — some steps may need adjustment."
fi

# ── 1. System packages ────────────────────────────────────────────────────────
log "--- Step 1: System packages ---"
need_root
apt-get update -q
apt-get install -y -q \
    build-essential curl wget git unzip \
    python3 python3-pip python3-venv python3-dev \
    pciutils lsb-release gnupg2 ca-certificates \
    pigz                      # parallel gzip for faster demo decompression
ok "System packages installed"

# ── 2. NVIDIA driver + CUDA 12.x ─────────────────────────────────────────────
log "--- Step 2: NVIDIA CUDA Toolkit ---"
if nvidia-smi &>/dev/null; then
    DRIVER_VER=$(nvidia-smi --query-gpu=driver_version --format=csv,noheader | head -1)
    ok "NVIDIA driver already installed (version $DRIVER_VER) — skipping"
else
    log "Installing NVIDIA driver + CUDA 12.4 ..."
    # CUDA keyring for Ubuntu 22.04
    CUDA_KEYRING="cuda-keyring_1.1-1_all.deb"
    wget -q "https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/x86_64/$CUDA_KEYRING" \
        -O "/tmp/$CUDA_KEYRING"
    dpkg -i "/tmp/$CUDA_KEYRING"
    apt-get update -q
    # cuda-12-4 pulls driver + toolkit; adjust version if you prefer 12.x
    apt-get install -y -q cuda-12-4
    rm -f "/tmp/$CUDA_KEYRING"

    # Add CUDA to PATH for this shell and future logins
    CUDA_PATH_LINE='export PATH=/usr/local/cuda/bin:$PATH'
    LD_LINE='export LD_LIBRARY_PATH=/usr/local/cuda/lib64:${LD_LIBRARY_PATH:-}'
    grep -qF "$CUDA_PATH_LINE" /etc/profile.d/cuda.sh 2>/dev/null || \
        printf '%s\n%s\n' "$CUDA_PATH_LINE" "$LD_LINE" | tee /etc/profile.d/cuda.sh
    source /etc/profile.d/cuda.sh
    ok "CUDA 12.4 installed — reboot recommended before first GPU training run"
fi

# ── 3. Go ─────────────────────────────────────────────────────────────────────
log "--- Step 3: Go $GO_VERSION ---"
if go version 2>/dev/null | grep -q "go$GO_VERSION"; then
    ok "Go $GO_VERSION already installed"
else
    GO_TGZ="go${GO_VERSION}.linux-amd64.tar.gz"
    wget -q "https://go.dev/dl/$GO_TGZ" -O "/tmp/$GO_TGZ"
    rm -rf /usr/local/go
    tar -C /usr/local -xzf "/tmp/$GO_TGZ"
    rm "/tmp/$GO_TGZ"
    ln -sf /usr/local/go/bin/go /usr/local/bin/go
    ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
    ok "Go $GO_VERSION installed"
fi

# ── 4. Build Go parser ────────────────────────────────────────────────────────
log "--- Step 4: Build Go parser binary ---"
(
    export PATH=/usr/local/go/bin:$PATH
    cd "$REPO_DIR"
    go build -o dem-parser .
)
ok "dem-parser binary built at $REPO_DIR/dem-parser"

# ── 5. Python venv + PyTorch CUDA + PyG ───────────────────────────────────────
log "--- Step 5: Python venv with PyTorch CUDA 12.1 + PyG ---"
# Detect Python 3 executable (prefer python3.11 or python3.10)
PYTHON3=$(command -v python3.11 || command -v python3.10 || command -v python3)
PY_VER=$("$PYTHON3" -c "import sys; print(f'{sys.version_info.major}.{sys.version_info.minor}')")
log "Using Python $PY_VER at $PYTHON3"

VENV="$GNN_DIR/venv"
if [[ ! -d "$VENV" ]]; then
    "$PYTHON3" -m venv "$VENV"
    ok "Venv created at $VENV"
fi

# Activate for the rest of this script
# shellcheck disable=SC1091
source "$VENV/bin/activate"

pip install -q --upgrade pip wheel

# PyTorch with CUDA 12.1 wheels (works on RTX 3060 with CUDA 12.x driver)
log "Installing PyTorch 2.4 (CUDA 12.1) ..."
pip install -q \
    torch==2.4.0 torchvision torchaudio \
    --index-url https://download.pytorch.org/whl/cu121

# PyG + scatter/sparse (must match PyTorch + CUDA versions)
log "Installing PyTorch Geometric ..."
TORCH_VER=$(python3 -c "import torch; print(torch.__version__.split('+')[0])")
CUDA_TAG="cu121"
pip install -q torch-geometric
pip install -q \
    torch-scatter torch-sparse torch-cluster torch-spline-conv \
    -f "https://data.pyg.org/whl/torch-${TORCH_VER}+${CUDA_TAG}.html"

# GNN + pipeline requirements
pip install -q -r "$GNN_DIR/requirements.txt"
pip install -q -r "$REPO_DIR/pipeline/requirements.txt"

ok "Python env ready"

# ── 6. Verify GPU is visible to PyTorch ───────────────────────────────────────
log "--- Step 6: GPU verification ---"
python3 - <<'PYCHECK'
import torch
assert torch.cuda.is_available(), "CUDA not available to PyTorch!"
name = torch.cuda.get_device_name(0)
vram = torch.cuda.get_device_properties(0).total_memory / 1024**3
print(f"  GPU: {name}")
print(f"  VRAM: {vram:.1f} GB")
print(f"  CUDA: {torch.version.cuda}")
assert vram >= 10, f"Expected ≥10 GB VRAM, got {vram:.1f}"
print("  GPU check passed ✓")
PYCHECK

# ── 7. Create directories ─────────────────────────────────────────────────────
log "--- Step 7: Directory layout ---"
mkdir -p \
    "$REPO_DIR/input" \
    "$REPO_DIR/output" \
    "$GNN_DIR/data/cache" \
    "$GNN_DIR/data/models" \
    "$REPO_DIR/pipeline/logs"

cat > "$REPO_DIR/.env.example" <<'ENVEOF'
# Copy to .env and fill in your values
FACEIT_API_KEY=your-key-here
MAX_DEMOS=40          # demos per cron run
BATCH_SIZE=40         # demos to process before deleting scratch files
RETRAIN_THRESHOLD=50  # retrain after this many cached demos
DELETE_DEMOS=true     # delete .dem files after graph caching (saves ~500MB/demo)
DELETE_CSVS=true      # delete CSVs after graph caching (saves ~260MB/demo)
ENVEOF
ok "Directories and .env.example created"

# ── 8. Disk budget summary ────────────────────────────────────────────────────
log "--- Disk budget for 500 GB ---"
cat <<'EOF'
  Item                          Size        Deletable after?
  ─────────────────────────────────────────────────────────
  OS + software                  ~30 GB     No
  Go binary + repo               ~0.5 GB    No
  Python venv (PyTorch CUDA)     ~5 GB      No
  Per-run batch (40 demos)       ~30 GB     Yes — delete after graph cache
    └─ Raw .dem files            20 GB      → delete after Go parse
    └─ Output CSVs               10 GB      → delete after build_graphs.py
  Graph cache (all demos .pt)    ~2.5 GB    No — this is the training input
  Model checkpoints              ~0.5 GB    No
  ─────────────────────────────────────────────────────────
  Working budget per batch       ~35 GB     (safe on 500 GB)
  Max demos you can cache        unlimited  (only .pt files accumulate)
EOF

# ── 9. Register cron job ──────────────────────────────────────────────────────
log "--- Step 9: Optional cron setup ---"
CRON_LINE="0 3 * * * bash $REPO_DIR/pipeline/local_ubuntu_run.sh >> $REPO_DIR/pipeline/logs/cron.log 2>&1"
log "To add daily cron job, run:"
echo ""
echo "  crontab -e"
echo "  # Paste this line:"
echo "  $CRON_LINE"
echo ""

# ── Done ──────────────────────────────────────────────────────────────────────
log "=== Setup complete ==="
log "Next steps:"
log "  1. Set your API key:  export FACEIT_API_KEY='your-key'"
log "     (add to ~/.bashrc for persistence)"
log "  2. Run the pipeline:  bash pipeline/local_ubuntu_run.sh"
log "  3. Or add to cron:    see Step 9 above"
