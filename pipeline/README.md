# CS2 Tactic Prediction — Pipeline

End-to-end automation: download Faceit CS2 pro demos → Go parser → PyG graph cache → GNN training.

Data source: **Faceit Open Data API** (official, 1,000 req/hr free tier, no scraping, no blocks).

```
[Faceit API] → input/*.dem
                    ↓
             [Go parser --no-clear] → output/*.csv
                    ↓
          [build_graphs.py --incremental] → gnn/data/cache/*.pt
                    ↓
            delete .dem + .csv (reclaim disk)
                    ↓
              [train.py --graphs-dir] → gnn/data/models/
```

---

## Files in this directory

| File | Purpose |
|---|---|
| `ubuntu_setup.sh` | One-time setup for Ubuntu + RTX 3060 (CUDA, Go, Python venv) |
| `local_ubuntu_run.sh` | Daily cron-ready pipeline for local Ubuntu machine |
| `faceit_download.py` | Faceit Open API downloader (state-tracked, idempotent) |
| `orchestrate.py` | Prefect flow wrapping all stages (local or cloud training) |
| `cloud_train.sh` | Spin up Vast.ai RTX 4090, train, download models, destroy instance |
| `cron_pipeline.sh` | Minimal cron wrapper (download → parse → cache → optional cloud train) |
| `requirements.txt` | Pipeline Python deps (prefect, requests, tqdm) |

---

## Option A — Local Ubuntu machine (RTX 3060, 30 GB RAM, 500 GB disk)

This is the recommended path if you own a machine with a CUDA GPU. Zero cloud cost.

### Step 1 — One-time setup (run as root)

```bash
git clone <your-repo> dem-parser && cd dem-parser
sudo bash pipeline/ubuntu_setup.sh
```

What it installs:
- CUDA 12.4 driver + toolkit
- Go 1.22.4 (builds the `dem-parser` binary)
- Python venv at `gnn/venv/` with PyTorch 2.4 + CUDA 12.1 wheels + PyTorch Geometric

### Step 2 — Configure API key

Get a **free** Faceit API key at [developers.faceit.com/apps](https://developers.faceit.com/apps).

```bash
# Add to ~/.bashrc (persists across reboots and cron runs)
echo 'export FACEIT_API_KEY="your-key-here"' >> ~/.bashrc
source ~/.bashrc
```

Or create a `.env` file in the repo root (auto-loaded by the run script):

```bash
cat > .env <<'EOF'
FACEIT_API_KEY=your-key-here
BATCH_SIZE=40
RETRAIN_THRESHOLD=50
TRAIN_EPOCHS=500
DELETE_DEMOS=true
DELETE_CSVS=true
EOF
```

### Step 3 — Run the pipeline

```bash
bash pipeline/local_ubuntu_run.sh
```

Each run:
1. Downloads up to `$BATCH_SIZE` (default 40) new Faceit pro demos
2. Parses each demo with the Go binary (`--no-clear` incremental mode)
3. Caches graphs as one `.pt` per demo in `gnn/data/cache/`
4. **Immediately deletes** the `.dem` (~500 MB) and CSVs (~260 MB) after caching
5. When cache reaches `$RETRAIN_THRESHOLD` demos → trains per-map GNN on the RTX 3060
6. Saves versioned checkpoints to `gnn/data/models/`

**Disk budget per batch (40 demos):**

```
40 × 500 MB demos          = 20 GB   (deleted after parse)
40 × 260 MB CSVs           = 10 GB   (deleted after graph cache)
Peak scratch               = ~30 GB
Graph cache (all time)     = ~2.5 GB (for 1,558 demos)
Usable for demos           = 460 GB  → safe for thousands of runs
```

### Step 4 — Add to cron (daily auto-run)

```bash
crontab -e
```

Paste this line (adjust path):

```
0 3 * * * bash /home/user/dem-parser/pipeline/local_ubuntu_run.sh >> /home/user/dem-parser/pipeline/logs/cron.log 2>&1
```

Runs at 03:00 every night. Downloads 40 demos, processes them, trains when threshold is reached.

### Tuning for RTX 3060

The RTX 3060 is detected automatically. Training config applied:

| Setting | Value | Reason |
|---|---|---|
| Device | `cuda` (auto) | PyTorch detects RTX 3060 |
| VRAM used | < 2 GB | GNN graphs are tiny (13 nodes × 25 features) |
| Training speed | ~1 min / epoch | vs ~15 min/epoch on CPU |
| `batch_size` | 32 | Comfortable in 12 GB VRAM |
| Workers (Go parse) | `nproc - 2` | Leaves headroom for OS |

---

## Option B — Vast.ai cloud GPU (cheapest pay-as-you-go)

Use this when you want to train at scale without owning a GPU, or to run a one-off bulk training session.

**Core insight:** build graphs locally (or on a cheap CPU), upload only the 2.5 GB `.pt` cache to the cloud, pay for GPU time only (~4 hours × $0.30/hr = **$1.20** for 1,558 demos).

### Prerequisites

```bash
pip install vastai
vastai set api-key YOUR_VAST_KEY   # get at https://vast.ai/console/account
```

### Run

```bash
# Build graph cache locally first (or use existing cache)
bash pipeline/local_ubuntu_run.sh   # or cron_pipeline.sh

# Then upload cache and train on cheapest RTX 4090
bash pipeline/cloud_train.sh
```

`cloud_train.sh` does everything automatically:
1. Packages `gnn/data/cache/` into a tarball
2. Finds the cheapest RTX 4090 under `$0.40/hr` (falls back to RTX 3090)
3. Creates instance, waits for SSH
4. Uploads the cache tarball (~2.5 GB)
5. Runs `train.py --graphs-dir cache/` on the instance
6. Downloads model checkpoints back to `gnn/data/models/`
7. **Destroys the instance** (billing stops automatically — no forgotten instances)

Override GPU or price ceiling:

```bash
VAST_GPU="RTX_3090" VAST_MAX_PRICE="0.25" bash pipeline/cloud_train.sh
TRAIN_MODE="--full" TRAIN_EPOCHS="2000" bash pipeline/cloud_train.sh  # 100% target
```

### Cloud cost reference

| Scenario | Cost | Time |
|---|---|---|
| Train from 50-demo cache (RTX 4090) | ~$0.15 | ~25 min |
| Train from 200-demo cache (RTX 4090) | ~$0.60 | ~1.5 hr |
| Train from full ESTA cache — 1,558 demos (RTX 4090) | ~$1.50 | ~4 hr |
| Parse 1,558 demos on Lambda Labs CPU (16 vCPU) | ~$8 | ~10 hr |

---

## Option C — Prefect scheduled flow (local or cloud)

Use this for fully automated unattended operation with a UI dashboard.

```bash
# Install Prefect
source gnn/venv/bin/activate
pip install prefect>=3.0

# Start Prefect server (keep running in background / tmux)
prefect server start &

# Register daily schedule (03:00 UTC, local training)
python3 pipeline/orchestrate.py --deploy --max-demos 40

# With Vast.ai cloud training
python3 pipeline/orchestrate.py --deploy --max-demos 40 --cloud-train

# Start the worker
prefect worker start -p default-process-work-pool
```

Open the Prefect UI at `http://localhost:4200` to monitor runs, view logs, and inspect failures.

One-shot manual runs (no server needed):

```bash
# Download + parse + cache + train locally
python3 pipeline/orchestrate.py --max-demos 20

# Download + parse + cache + train on Vast.ai
python3 pipeline/orchestrate.py --max-demos 20 --cloud-train

# Retrain immediately from existing cache (no download)
python3 pipeline/orchestrate.py --force-retrain
python3 pipeline/orchestrate.py --force-retrain --cloud-train

# Filter to specific maps only
python3 pipeline/orchestrate.py --max-demos 50 --maps inferno nuke overpass
```

---

## Individual commands

### Download only

```bash
source gnn/venv/bin/activate
python3 pipeline/faceit_download.py --limit 20
python3 pipeline/faceit_download.py --limit 20 --maps inferno nuke
python3 pipeline/faceit_download.py --list-championships   # discover available leagues
```

State is tracked in `pipeline/faceit_downloaded.txt`. Re-running always skips already-downloaded matches.

### Parse only

```bash
./dem-parser --input input/ --output output/ --no-clear
```

`--no-clear` is required for incremental runs — without it, all existing CSVs are deleted on start.

### Build graph cache only

```bash
source gnn/venv/bin/activate

# Build from CSV output (first time or after new parse)
python3 gnn/build_graphs.py output/ --incremental --cache-dir gnn/data/cache/

# Load all cached demos and check totals
python3 -c "
from gnn.build_graphs import load_graphs_per_map_from_cache
data = load_graphs_per_map_from_cache('gnn/data/cache/')
for m, v in data.items():
    print(f'{m}: {len(v[\"graphs\"])} graphs | classes: {v[\"classes\"]}')
"
```

### Train only

```bash
source gnn/venv/bin/activate

# Per-map eval mode (80/20 split) — best for accuracy benchmarking
python3 gnn/train.py --graphs-dir gnn/data/cache/

# Per-map eval mode with custom epoch count
python3 gnn/train.py --graphs-dir gnn/data/cache/ --epochs 1000

# Full-dataset mode — trains on all data, targets 100% training accuracy
python3 gnn/train.py --graphs-dir gnn/data/cache/ --full

# Cross-map single model
python3 gnn/train.py --graphs-dir gnn/data/cache/ --cross-map
```

---

## Expected accuracy trajectory

| Demos / map | Graph cache size | Test accuracy |
|---|---|---|
| 3 (current) | ~2.5 MB | ~60% |
| 50 | ~40 MB | ~75–80% |
| 200 | ~160 MB | ~85–90% |
| 1,000+ | ~800 MB | ~90–95% |
| Full ESTA (~220/map) | ~2.5 GB | ~90–95% |

Training accuracy of 100% is already proven on 3 demos. Test accuracy is limited by dataset size — each new demo batch meaningfully closes the gap.

---

## Troubleshooting

**`FACEIT_API_KEY is not set`**
```bash
export FACEIT_API_KEY="your-key"   # or add to ~/.bashrc
```

**`Go binary missing`**
```bash
go build -o dem-parser .
```

**`CUDA not visible to PyTorch`**

Reboot after CUDA installation, then verify:
```bash
source gnn/venv/bin/activate
python3 -c "import torch; print(torch.cuda.get_device_name(0))"
```

If still failing, reinstall PyTorch with the correct CUDA version:
```bash
pip install torch==2.4.0 --index-url https://download.pytorch.org/whl/cu121
```

**`No .pt files found in cache/`**

Run Stage 3 first:
```bash
python3 gnn/build_graphs.py output/ --incremental --cache-dir gnn/data/cache/
```

**`Only X GB free — need ≥35 GB`**

The disk guard triggers when scratch space is too low for a batch.
Lower the batch size or free disk space:
```bash
BATCH_SIZE=10 bash pipeline/local_ubuntu_run.sh
```

**Vast.ai: `No suitable GPU instance found`**

Raise the price ceiling or try a different GPU:
```bash
VAST_GPU="RTX_3090" VAST_MAX_PRICE="0.30" bash pipeline/cloud_train.sh
```
