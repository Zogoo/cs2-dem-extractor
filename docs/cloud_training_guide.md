# Cloud Training Guide — Cheapest Path to a 100% GNN Model

## The Core Cost Problem

The naive pipeline is expensive because it moves raw data through the cloud:

```
.dem files (2.5 TB) → parse CSVs (1.3 TB) → build graphs (2.5 GB) → train
```

The optimised pipeline moves only the graphs:

```
.dem files (local) → parse CSVs (local) → .pt cache (2.5 GB) → upload → train in cloud
```

Uploading 2.5 GB of `.pt` files to a cloud GPU and downloading model checkpoints (~100 MB)
is essentially free. This eliminates the need for 3 TB cloud NVMe and most of the egress cost.

---

## Part 1 — Getting Demos Without HLTV Throttling

HLTV blocks automated scrapers within minutes. The `pipeline/download.py` script
will hit HTTP 429 or Cloudflare blocks on most IPs. Use these alternatives instead.

### Option A — ESTA Dataset (best starting point, free, no scraping)

The ESTA dataset is 1,558 professional CS:GO match demos released under CC BY-SA 4.0.
No scraping, no throttling, direct academic download.

```bash
# Install the esta Python package
pip install esta

# List available matches (metadata only, fast)
python3 -c "import esta; print(esta.list_matches()[:5])"

# Download all demos to input/ (this takes hours — run overnight or in cloud)
python3 -c "
import esta, os
os.makedirs('input', exist_ok=True)
matches = esta.list_matches()
for m in matches[:100]:                # Start with 100
    esta.download_demo(m, directory='input/')
"
```

Or clone the dataset index and download via HTTP directly:

```
Dataset URL: https://github.com/pnxenopoulos/esta
Mirror:      https://zenodo.org/record/XXXXXXX   (check esta repo for current link)
Size:        ~779 GB total, ~500 MB/demo
Maps:        Dust2, Inferno, Mirage, Nuke, Overpass, Vertigo, Ancient
Period:      Jan 2021 – May 2022 (CS:GO)
```

ESTA demos parse cleanly with the existing Go parser. No label changes needed —
`tactic_extractor.go` labels them automatically.

### Option B — Faceit API (CS2 demos, legitimate API, no scraping)

Faceit has an official API with 1,000 req/hr rate limit — no throttling issues.

```bash
pip install faceit-data    # community wrapper, or use requests directly
```

```python
import requests, os, time

API_KEY = "YOUR_FACEIT_API_KEY"   # free at developers.faceit.com
HEADERS = {"Authorization": f"Bearer {API_KEY}"}
BASE = "https://open.faceit.com/data/v4"

def get_pro_matches(limit=100):
    """Fetch competitive CS2 match IDs from Faceit."""
    resp = requests.get(f"{BASE}/championships", headers=HEADERS,
                        params={"game": "cs2", "type": "pro", "limit": limit})
    return resp.json()

def download_demo(match_id: str, dest_dir: str):
    """Download a single match demo via Faceit demo URL."""
    resp = requests.get(f"{BASE}/matches/{match_id}", headers=HEADERS)
    data = resp.json()
    demo_url = data.get("demo_url", [None])[0]
    if not demo_url:
        return
    r = requests.get(demo_url, stream=True, timeout=120)
    path = os.path.join(dest_dir, f"{match_id}.dem.gz")
    with open(path, "wb") as f:
        for chunk in r.iter_content(8192):
            f.write(chunk)
    # Decompress
    import gzip, shutil
    with gzip.open(path) as gz, open(path.replace(".gz",""), "wb") as out:
        shutil.copyfileobj(gz, out)
    os.remove(path)
    time.sleep(1)   # stay under 1,000/hr limit
```

Faceit Pro League covers top-tier CS2 (BLAST, ESL, etc.) since 2023.

### Option C — Manual Download + Bulk Upload (most reliable, no risk)

1. Log in to HLTV in your browser.
2. Download demos manually from match pages (1 click per demo, no rate limiting on logged-in sessions).
3. Batch-upload to cloud storage:

```bash
# Compress locally first (halves upload size)
tar -czf demos_batch1.tar.gz input/*.dem

# Upload to Vast.ai / Lambda Labs instance via scp
scp demos_batch1.tar.gz user@<instance-ip>:~/dem-parser/

# Or upload to Cloudflare R2 (free egress, $0.015/GB storage)
aws s3 cp demos_batch1.tar.gz s3://your-bucket/ --endpoint-url https://...r2.cloudflarestorage.com
```

### Option D — HLTV with Authenticated Session (if automation needed)

If you must scrape HLTV, use your real browser session cookies to avoid blocks:

```python
import requests, browser_cookie3

# Pull cookies from your logged-in Chrome/Firefox session
cookies = browser_cookie3.chrome(domain_name=".hltv.org")
session = requests.Session()
session.cookies.update(cookies)

# Now requests look like your real browser
resp = session.get("https://www.hltv.org/results", headers={
    "User-Agent": "Mozilla/5.0 ...",
    "Referer": "https://www.hltv.org",
})
```

Additional measures to avoid detection:
- Use `time.sleep(random.uniform(4, 9))` between requests (not fixed 3s)
- Rotate `User-Agent` strings from a real browser list
- Run through a residential proxy if doing large-scale scraping:
  ```
  Oxylabs:   ~$8/GB (overkill for this use case)
  Webshare:  ~$2.99/mo for 1 GB residential (enough for metadata)
  ```
- Start each session on HLTV's homepage before hitting listing pages

---

## Part 2 — The Cheapest Cloud Workflow

### Recommended: Build graphs locally, train in cloud

This is the cheapest path because you only pay for GPU time (a few hours),
not for storage or egress of raw demos.

```
Local machine:
  1. Place .dem files in input/
  2. ./dem-parser --input input/ --output output/ --no-clear
  3. python3 gnn/build_graphs.py output/ --incremental --cache-dir gnn/data/cache/
  4. tar -czf graph_cache.tar.gz gnn/data/cache/   # ~2.5 GB for 1,558 demos

Cloud GPU (Vast.ai / RunPod):
  5. Upload graph_cache.tar.gz
  6. python3 gnn/train.py --graphs-dir cache/ --epochs 500
  7. Download best_model_*.pt files (~50 MB each)
```

Step 2–3 on a MacBook with 3 demos takes under 5 minutes.
At 1,558 demos it takes ~48 hrs on a 16-core machine, or ~6 hrs on Vast.ai.
Parse locally if you have the CPU; only upload the small `.pt` cache.

### Platform selection

| Platform | GPU | $/hr | Egress | Best for |
|---|---|---|---|---|
| **Vast.ai** | RTX 4090 | $0.20–0.35 | Varies by host | Cheapest single runs |
| **RunPod Spot** | RTX 4090 | $0.34 | $0.09/GB | Reliable spot instances |
| **Lambda Labs** | RTX 6000 Ada | $0.69 | **Free** | Sustained runs with large data |
| Paperspace Gradient | A4000 | $0.76 | $0.05/GB | Jupyter-based workflow |
| Google Colab Pro+ | A100 | ~$1.00 | Free | Quickest to start |

**For training only (uploading 2.5 GB graph cache):** Vast.ai RTX 4090 at $0.30/hr.
Typical training run on 1,558 demos: 4–6 hours = **$1.20–$1.80 total**.

**For full pipeline in cloud (parsing + training):** Lambda Labs, no egress fees.

---

## Part 3 — Step-by-Step: Vast.ai (cheapest training)

### 1. Set up Vast.ai

```bash
# Install CLI
pip install vastai
vastai set api-key YOUR_KEY_FROM_VAST_AI_CONSOLE
```

### 2. Find a cheap RTX 4090 instance

```bash
# List instances: min 16 GB VRAM, RTX 4090, under $0.40/hr, SSD storage
vastai search offers 'gpu_name=RTX_4090 num_gpus=1 disk_space>=50 dph_total<0.40' \
  --order dph_total --limit 5
```

### 3. Create and connect

```bash
# Create instance (replace OFFER_ID with id from search results)
vastai create instance OFFER_ID \
  --image pytorch/pytorch:2.2.0-cuda12.1-cudnn8-runtime \
  --disk 60 \
  --onstart-cmd "pip install torch-geometric requests beautifulsoup4 tqdm"

# Get SSH command
vastai ssh-url INSTANCE_ID

# Connect
ssh -p PORT root@INSTANCE_IP
```

### 4. Upload graph cache and train

```bash
# On local machine — compress cache
tar -czf graph_cache.tar.gz gnn/data/cache/

# Upload (replace with your instance details)
scp -P PORT graph_cache.tar.gz root@INSTANCE_IP:~/

# On cloud instance
mkdir -p dem-parser/cache
tar -xzf graph_cache.tar.gz -C dem-parser/cache/

# Clone repo (or scp the Python files)
git clone YOUR_REPO dem-parser
cd dem-parser/gnn
pip install -r requirements.txt

# Train
python3 train.py --graphs-dir ../cache/ --epochs 500

# Download model when done (on local machine)
scp -P PORT root@INSTANCE_IP:~/dem-parser/gnn/data/best_model_*.pt gnn/data/
```

### 5. Destroy instance when done

```bash
vastai destroy instance INSTANCE_ID
```

---

## Part 4 — Step-by-Step: Lambda Labs (full pipeline)

Use Lambda Labs when you want to run parsing + training entirely in the cloud
(eliminates local CPU burden for 1,558 demos).

### 1. Launch instance

At [lambdalabs.com/service/gpu-cloud](https://lambdalabs.com/service/gpu-cloud):
- **Instance type:** `gpu_1x_rtx6000ada` ($0.69/hr)
- **Storage:** Attach a 2 TB persistent volume (keeps data across restarts)
- **OS:** Lambda Stack (Ubuntu 22.04, CUDA pre-installed)

### 2. Setup

```bash
# SSH in
ssh ubuntu@<instance-ip>

# Go
wget -q https://go.dev/dl/go1.22.3.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.22.3.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc && source ~/.bashrc

# Clone repo
git clone YOUR_REPO dem-parser && cd dem-parser
go build -o dem-parser .

# Python env
python3 -m venv gnn/venv
source gnn/venv/bin/activate
pip install -r gnn/requirements.txt
pip install -r pipeline/requirements.txt
```

### 3. Load data

```bash
# Option A: upload local demos via scp
scp -r input/ ubuntu@<instance-ip>:~/dem-parser/

# Option B: download ESTA directly on the instance (Lambda has fast internet)
pip install esta
python3 -c "
import esta, os
os.makedirs('input', exist_ok=True)
for m in esta.list_matches():
    esta.download_demo(m, directory='input/')
"
```

### 4. Run full pipeline

```bash
# Parse all demos
./dem-parser --input input/ --output output/ --no-clear

# Build graph cache
source gnn/venv/bin/activate
python3 gnn/build_graphs.py output/ --incremental --cache-dir gnn/data/cache/

# Delete CSVs to free space
rm output/*_map_activities.csv output/*_round_tactics.csv output/*_game_events.csv

# Train (per-map, eval mode)
python3 gnn/train.py --graphs-dir gnn/data/cache/ --epochs 500

# Or full-dataset mode (targets 100% train accuracy)
python3 gnn/train.py --full --graphs-dir gnn/data/cache/ --epochs 2000
```

### 5. Download models

```bash
# On local machine
scp ubuntu@<instance-ip>:~/dem-parser/gnn/data/best_model_*.pt gnn/data/
```

Total Lambda cost for 1,558 demos: parse (~8 hrs) + train (~4 hrs) × $0.69 = **~$8**.

---

## Part 5 — Cost Summary

| Scenario | Platform | Data source | Est. cost | Time |
|---|---|---|---|---|
| Train only, 1,558 demos | Vast.ai RTX 4090 | ESTA (local parse) | **~$2** | 4–6 hrs GPU |
| Full pipeline, 1,558 demos | Lambda Labs | ESTA (cloud download) | **~$8** | 12 hrs total |
| Full pipeline, 5,000 demos | Lambda Labs | HLTV manual batch | **~$140** | ~2 days |
| Quick test, 3 current demos | Local (MPS/CPU) | Already in input/ | **$0** | <10 min |

### Expected accuracy at each scale

| Demos / map | Test accuracy |
|---|---|
| 3 (current) | ~60% |
| 50 | ~75–80% |
| 200 | ~85–90% |
| 1,000+ | ~90–95% |
| Full ESTA (~220/map) | ~90–95% |

---

## Quick Reference — Command Cheatsheet

```bash
# Local: parse + cache (incremental, safe to re-run)
./dem-parser --input input/ --output output/ --no-clear
python3 gnn/build_graphs.py output/ --incremental --cache-dir gnn/data/cache/

# Local: train from cache
source gnn/venv/bin/activate
python3 gnn/train.py --graphs-dir gnn/data/cache/

# Cloud: upload cache and train (Vast.ai example)
tar -czf cache.tar.gz gnn/data/cache/
scp -P PORT cache.tar.gz root@HOST:~/
# ... on cloud: tar -xzf cache.tar.gz && python3 train.py --graphs-dir cache/

# Run full automated pipeline (local or cloud)
python3 pipeline/orchestrate.py --max-demos 50

# Force retrain from existing cache (no download/parse)
python3 pipeline/orchestrate.py --force-retrain
```
