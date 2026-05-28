"""
Prefect orchestration flow for the CS2 tactic prediction pipeline.

6-stage automated pipeline:
  1. Download Faceit CS2 professional demos → input/
  2. Go parser → output/ CSVs (--no-clear for incremental)
  3. Build per-demo .pt graph caches → gnn/data/cache/
  4. Delete CSVs to reclaim disk space
  5. Retrain GNN — locally or via Vast.ai cloud (--cloud-train)
  6. Evaluate and save versioned model checkpoints

Data source: Faceit Open Data API (official, 1,000 req/hr free tier, no scraping).
  Get API key: https://developers.faceit.com/apps
  Set env var: export FACEIT_API_KEY="your-key"

Usage:
  # Manual one-shot run (local training)
  python3 pipeline/orchestrate.py --max-demos 20

  # One-shot with Vast.ai cloud training
  python3 pipeline/orchestrate.py --max-demos 20 --cloud-train

  # Force retrain from existing cache only (no download/parse)
  python3 pipeline/orchestrate.py --force-retrain
  python3 pipeline/orchestrate.py --force-retrain --cloud-train

  # Deploy as a daily scheduled Prefect flow
  prefect server start                   # in another terminal
  python3 pipeline/orchestrate.py --deploy --max-demos 50

Requirements:
  pip install prefect>=3.0 requests tqdm
  export FACEIT_API_KEY="your-key"
  Go binary built: cd <repo> && go build -o dem-parser .
  Vast.ai (for --cloud-train): pip install vastai && vastai set api-key YOUR_KEY
"""

import argparse
import glob
import os
import subprocess
import sys
from pathlib import Path

from prefect import flow, task, get_run_logger
from prefect.schedules import CronSchedule

_ROOT = Path(__file__).parent.parent
_GNN_DIR = _ROOT / "gnn"
_PIPELINE_DIR = _ROOT / "pipeline"
_INPUT_DIR = _ROOT / "input"
_OUTPUT_DIR = _ROOT / "output"
_CACHE_DIR = _GNN_DIR / "data" / "cache"
_MODELS_DIR = _GNN_DIR / "data" / "models"
_STATE_FILE = _PIPELINE_DIR / "faceit_downloaded.txt"   # Faceit match ID state
_GO_BINARY = _ROOT / "dem-parser"

# Retrain trigger: when this many new demos have accumulated in the cache.
RETRAIN_THRESHOLD = 20

# Python inside the GNN venv; falls back to system python3.
_VENV_PYTHON = _GNN_DIR / "venv" / "bin" / "python3"
PYTHON = str(_VENV_PYTHON) if _VENV_PYTHON.exists() else sys.executable


# ── Tasks ──────────────────────────────────────────────────────────────────────

@task(name="faceit-download", retries=2, retry_delay_seconds=60)
def download_demos(max_demos: int, api_key: str,
                   map_filter: list[str] | None = None) -> int:
    """Download up to max_demos new CS2 professional demos from Faceit API."""
    logger = get_run_logger()
    logger.info(f"Fetching up to {max_demos} demo(s) from Faceit API ...")

    cmd = [
        PYTHON, str(_PIPELINE_DIR / "faceit_download.py"),
        "--limit",  str(max_demos),
        "--output", str(_INPUT_DIR),
        "--state",  str(_STATE_FILE),
        "--api-key", api_key,
    ]
    if map_filter:
        cmd += ["--maps"] + map_filter

    env = {**os.environ, "FACEIT_API_KEY": api_key}
    result = subprocess.run(cmd, capture_output=True, text=True, env=env)
    logger.info(result.stdout)
    if result.returncode != 0:
        logger.error(result.stderr)
        raise RuntimeError(f"faceit_download.py failed (exit {result.returncode})")

    dem_count = len(list(_INPUT_DIR.glob("*.dem")))
    logger.info(f"input/ now holds {dem_count} .dem file(s)")
    return dem_count


@task(name="go-parse", retries=1)
def go_parse() -> list[str]:
    """
    Run the Go parser on all .dem files in input/.
    --no-clear keeps existing output/ CSVs (incremental processing).
    Returns list of newly-produced base names.
    """
    logger = get_run_logger()

    if not _GO_BINARY.exists():
        raise FileNotFoundError(
            f"Go binary not found at {_GO_BINARY}. "
            f"Run: cd {_ROOT} && go build -o dem-parser ."
        )

    existing = set(glob.glob(str(_OUTPUT_DIR / "*_map_activities.csv")))

    logger.info(f"Parsing demos in {_INPUT_DIR} ...")
    result = subprocess.run(
        [str(_GO_BINARY),
         "--input",  str(_INPUT_DIR),
         "--output", str(_OUTPUT_DIR),
         "--no-clear"],
        capture_output=True, text=True, cwd=str(_ROOT),
    )
    logger.info(result.stdout[-3000:])
    if result.returncode != 0:
        logger.error(result.stderr)
        raise RuntimeError(f"Go parser failed (exit {result.returncode})")

    new_files = set(glob.glob(str(_OUTPUT_DIR / "*_map_activities.csv"))) - existing
    new_bases = [Path(f).stem.replace("_map_activities", "") for f in new_files]
    logger.info(f"Parsed {len(new_bases)} new demo(s)")
    return new_bases


@task(name="build-graph-cache")
def build_graph_cache(new_bases: list[str]) -> int:
    """Cache each new demo as a per-demo .pt graph file (idempotent)."""
    logger = get_run_logger()
    if not new_bases:
        logger.info("No new demos to cache.")
        return len(list(_CACHE_DIR.glob("*.pt")))

    _CACHE_DIR.mkdir(parents=True, exist_ok=True)
    logger.info(f"Building graph cache for {len(new_bases)} demo(s) ...")

    result = subprocess.run(
        [PYTHON, str(_GNN_DIR / "build_graphs.py"),
         str(_OUTPUT_DIR),
         "--incremental",
         "--cache-dir", str(_CACHE_DIR)],
        capture_output=True, text=True, cwd=str(_GNN_DIR),
    )
    logger.info(result.stdout)
    if result.returncode != 0:
        logger.error(result.stderr)
        raise RuntimeError(f"build_graphs.py failed (exit {result.returncode})")

    total = len(list(_CACHE_DIR.glob("*.pt")))
    logger.info(f"Cache total: {total} demo(s)")
    return total


@task(name="delete-csvs")
def delete_csvs(new_bases: list[str]):
    """Delete intermediate CSV files to reclaim disk space after graph caching."""
    logger = get_run_logger()
    suffixes = ["_map_activities.csv", "_round_tactics.csv",
                "_game_events.csv", "_tactical_events.csv", "_round_summaries.csv"]
    deleted = 0
    for base in new_bases:
        for suffix in suffixes:
            f = _OUTPUT_DIR / (base + suffix)
            if f.exists():
                f.unlink()
                deleted += 1
    logger.info(f"Deleted {deleted} CSV file(s) for {len(new_bases)} demo(s)")


@task(name="retrain-local", timeout_seconds=7200)
def retrain_local(train_mode: str = ""):
    """Train per-map GNN models locally using the full graph cache."""
    logger = get_run_logger()
    _MODELS_DIR.mkdir(parents=True, exist_ok=True)

    cmd = [PYTHON, str(_GNN_DIR / "train.py"),
           "--graphs-dir", str(_CACHE_DIR)]
    if train_mode:
        cmd.append(train_mode)

    logger.info(f"Training locally (mode={train_mode or 'eval'}) ...")
    result = subprocess.run(cmd, capture_output=True, text=True, cwd=str(_GNN_DIR))
    logger.info(result.stdout[-5000:])
    if result.returncode != 0:
        logger.error(result.stderr)
        raise RuntimeError(f"train.py failed (exit {result.returncode})")

    _archive_models(logger)


@task(name="retrain-cloud", timeout_seconds=10800)
def retrain_cloud():
    """Package graph cache and train on Vast.ai (cheapest GPU path)."""
    logger = get_run_logger()
    cloud_script = _PIPELINE_DIR / "cloud_train.sh"
    if not cloud_script.exists():
        raise FileNotFoundError(f"cloud_train.sh not found at {cloud_script}")

    logger.info("Launching Vast.ai cloud training ...")
    result = subprocess.run(
        ["bash", str(cloud_script)],
        capture_output=True, text=True,
    )
    logger.info(result.stdout[-5000:])
    if result.returncode != 0:
        logger.error(result.stderr)
        raise RuntimeError(f"cloud_train.sh failed (exit {result.returncode})")
    logger.info("Cloud training complete. Models in gnn/data/models/")


@task(name="evaluate-models")
def evaluate_models():
    """Run evaluate.py on current best_model_*.pt files, if the script exists."""
    logger = get_run_logger()
    eval_script = _GNN_DIR / "evaluate.py"
    if not eval_script.exists():
        logger.warning("evaluate.py not found — skipping evaluation.")
        return

    result = subprocess.run(
        [PYTHON, str(eval_script)],
        capture_output=True, text=True, cwd=str(_GNN_DIR),
    )
    logger.info(result.stdout[-3000:])
    if result.returncode != 0:
        logger.warning(f"evaluate.py exited {result.returncode}: {result.stderr[-500:]}")


# ── Helper ─────────────────────────────────────────────────────────────────────

def _archive_models(logger):
    """Copy best_model_*.pt into models/ with a version timestamp."""
    import shutil, time
    version = time.strftime("%Y%m%d_%H%M%S")
    _MODELS_DIR.mkdir(parents=True, exist_ok=True)
    for model_file in (_GNN_DIR / "data").glob("best_model_*.pt"):
        dest = _MODELS_DIR / f"{model_file.stem}_v{version}.pt"
        shutil.copy2(model_file, dest)
        logger.info(f"Archived: {dest.name}")


# ── Flows ──────────────────────────────────────────────────────────────────────

@flow(name="cs2-tactic-pipeline",
      description="Faceit download → Parse → Graph cache → Train CS2 GNN")
def pipeline(max_demos: int = 50,
             cloud_train: bool = False,
             map_filter: list[str] | None = None):
    """
    Full automated pipeline.

    Args:
        max_demos:   Max new demos to download from Faceit per run.
        cloud_train: If True, train on Vast.ai instead of locally.
        map_filter:  Optional list of map names to restrict downloads
                     e.g. ["inferno", "nuke"].
    """
    logger = get_run_logger()
    api_key = os.environ.get("FACEIT_API_KEY", "")
    if not api_key:
        raise EnvironmentError(
            "FACEIT_API_KEY is not set. "
            "Get a free key at https://developers.faceit.com/apps"
        )

    logger.info(f"Pipeline: max_demos={max_demos} cloud={cloud_train}")

    # Stage 1 — Download
    download_demos(max_demos=max_demos, api_key=api_key, map_filter=map_filter)

    # Stage 2 — Parse
    new_bases = go_parse()
    if not new_bases:
        logger.info("No new demos parsed — done for this run.")
        return

    # Stage 3 — Graph cache
    cached_total = build_graph_cache(new_bases)

    # Stage 4 — Delete CSVs
    delete_csvs(new_bases)

    # Stage 5 — Retrain?
    if cached_total >= RETRAIN_THRESHOLD:
        logger.info(f"Cache at {cached_total} demos (≥{RETRAIN_THRESHOLD}) → training")
        if cloud_train:
            retrain_cloud()
        else:
            retrain_local()
        evaluate_models()
    else:
        logger.info(
            f"Cache at {cached_total} demos (threshold {RETRAIN_THRESHOLD}) "
            "— accumulating more data before training."
        )

    logger.info("Pipeline run complete.")


# ── CLI ────────────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="CS2 tactic GNN pipeline — Faceit download + train")
    parser.add_argument("--max-demos", type=int, default=50,
                        help="Max new demos to download per run (default: 50)")
    parser.add_argument("--cloud-train", action="store_true",
                        help="Train on Vast.ai instead of locally (cheapest GPU path)")
    parser.add_argument("--maps", nargs="+", metavar="MAP",
                        help="Restrict downloads to specific maps e.g. --maps inferno nuke")
    parser.add_argument("--force-retrain", action="store_true",
                        help="Skip download/parse and retrain immediately from cache")
    parser.add_argument("--deploy", action="store_true",
                        help="Register as a daily Prefect scheduled flow (02:00 UTC)")
    args = parser.parse_args()

    if args.force_retrain:
        print("Force-retrain from cache ...")
        if args.cloud_train:
            retrain_cloud()
        else:
            import logging
            class _FakeLogger:
                info = warning = error = staticmethod(print)
            retrain_local.fn("")   # call underlying function directly
        evaluate_models.fn()
    elif args.deploy:
        from prefect.deployments import Deployment
        deployment = Deployment.build_from_flow(
            flow=pipeline,
            name="daily-cs2-faceit-pipeline",
            schedule=CronSchedule(cron="0 2 * * *", timezone="UTC"),
            parameters={
                "max_demos": args.max_demos,
                "cloud_train": args.cloud_train,
                "map_filter": args.maps,
            },
        )
        deployment.apply()
        print("Deployment registered. Start Prefect worker:")
        print("  prefect worker start -p default-process-work-pool")
    else:
        pipeline(
            max_demos=args.max_demos,
            cloud_train=args.cloud_train,
            map_filter=args.maps,
        )
