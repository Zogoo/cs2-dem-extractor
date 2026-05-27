# Automated Cloud Training Pipeline

End-to-end pipeline: download HLTV demos → parse → build graphs → train GNN.

## Quick start (local)

```bash
# 1. Install pipeline deps into the GNN venv
source gnn/venv/bin/activate
pip install -r pipeline/requirements.txt

# 2. Build the Go parser
go build -o dem-parser .

# 3. Run the pipeline (downloads 10 demos, parses, caches, trains if ≥20 demos)
python3 pipeline/orchestrate.py --max-demos 10
```

To skip downloading and retrain immediately from cached graphs:

```bash
python3 pipeline/orchestrate.py --force-retrain
```

---

## Individual stages

### Stage 1 — Download demos

```bash
python3 pipeline/download.py --limit 10 --output input/
```

Tracks downloaded match IDs in `pipeline/downloaded.txt`. Re-running skips already-downloaded matches.

> **Note:** HLTV blocks automated scraping. For bulk historical data use the
> [ESTA dataset](https://github.com/pnxenopoulos/esta) (1,558 matches, CC BY-SA 4.0).
> Place extracted `.dem` files directly in `input/` and skip to Stage 2.

### Stage 2 — Parse demos

```bash
./dem-parser --input input/ --output output/ --no-clear
```

`--no-clear` appends to `output/` without deleting existing CSVs (required for incremental runs).

### Stage 3 — Build graph cache

```bash
python3 gnn/build_graphs.py output/ --incremental --cache-dir gnn/data/cache/
```

Saves one `.pt` file per demo. Idempotent — already-cached demos are skipped.

### Stage 4 — Train

```bash
# From CSV output (small scale)
python3 gnn/train.py

# From .pt cache (large scale / cloud)
python3 gnn/train.py --graphs-dir gnn/data/cache/

# Full-dataset mode (no test split, targets 100% train accuracy)
python3 gnn/train.py --full --graphs-dir gnn/data/cache/
```

---

## Cloud setup (Lambda Labs — recommended)

Lambda Labs has **no egress fees**, which is decisive when moving 1.3 TB of CSVs.

### Instance

| Field | Value |
|---|---|
| Instance | `gpu_1x_rtx6000ada` ($0.69/hr) |
| vCPU / RAM | 14 vCPU / 100 GB |
| Storage | 1× 1 TB NVMe + attach persistent 2 TB volume |
| OS | Ubuntu 22.04 + CUDA 12.x |

### Setup script

```bash
# On Lambda instance
git clone <your-repo> dem-parser && cd dem-parser

# Go
wget https://go.dev/dl/go1.22.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.22.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
go build -o dem-parser .

# Python
python3 -m venv gnn/venv
source gnn/venv/bin/activate
pip install -r gnn/requirements.txt
pip install -r pipeline/requirements.txt

# Run pipeline
python3 pipeline/orchestrate.py --max-demos 200
```

### Scheduled deployment (Prefect)

```bash
# Start Prefect server (runs in background)
prefect server start &

# Register daily 02:00 UTC schedule
python3 pipeline/orchestrate.py --deploy --max-demos 50

# Start Prefect worker
prefect worker start -p default-process-work-pool
```

---

## Hardware estimates (5,000 demos)

| Resource | Estimate |
|---|---|
| Raw .dem storage (delete after parse) | 2.5 TB |
| CSV scratch (delete after graph build) | 1.3 TB |
| PyG .pt graph files (keep) | ~2.5 GB |
| Model checkpoints | ~5 GB |
| **Working scratch minimum** | **3 TB NVMe** |
| Go parsing (16 cores) | ~10 hrs wall-clock |
| GNN training (RTX 6000 Ada) | ~3–6 hrs |
| **Lambda cost (one-time)** | **~$140** |

## Expected accuracy trajectory

| Demos/map | Expected test acc |
|---|---|
| 3 (current) | ~60% |
| 50 | ~75–80% |
| 200 | ~85–90% |
| 1,000+ | ~90–95% |
| Full ESTA (1,558 matches) | ~95%+ |
