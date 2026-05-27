"""
Prefect orchestration flow for the CS2 tactic prediction pipeline.

6-stage automated pipeline:
  1. Download HLTV demos → input/
  2. Go parser → output/ CSVs (--no-clear for incremental)
  3. Build per-demo .pt graph caches → gnn/data/cache/
  4. Delete CSVs to reclaim disk space
  5. Retrain GNN per-map (triggered when >= RETRAIN_THRESHOLD new demos)
  6. Evaluate and save versioned model checkpoints

Usage:
  # Run once manually
  python3 pipeline/orchestrate.py --max-demos 10

  # Run on a schedule via Prefect server
  prefect server start           # in another terminal
  python3 pipeline/orchestrate.py --deploy

Requires:
  pip install prefect>=3.0 requests beautifulsoup4 tqdm
  (Go binary must be built: cd dem-parser && go build -o dem-parser .)
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
_STATE_FILE = _PIPELINE_DIR / "downloaded.txt"
_GO_BINARY = _ROOT / "dem-parser"

# Trigger a retrain when at least this many new demos have been processed.
RETRAIN_THRESHOLD = 20

# Python executable inside the gnn venv (falls back to sys.executable)
_VENV_PYTHON = _GNN_DIR / "venv" / "bin" / "python3"
PYTHON = str(_VENV_PYTHON) if _VENV_PYTHON.exists() else sys.executable


# ── Tasks ─────────────────────────────────────────────────────────────────────

@task(name="download-demos", retries=2, retry_delay_seconds=30)
def download_demos(max_demos: int) -> int:
    """Download up to max_demos new professional HLTV demos into input/."""
    logger = get_run_logger()
    logger.info(f"Downloading up to {max_demos} new demo(s) ...")

    result = subprocess.run(
        [PYTHON, str(_PIPELINE_DIR / "download.py"),
         "--limit", str(max_demos),
         "--output", str(_INPUT_DIR),
         "--state", str(_STATE_FILE)],
        capture_output=True, text=True
    )
    logger.info(result.stdout)
    if result.returncode != 0:
        logger.error(result.stderr)
        raise RuntimeError(f"download.py failed (exit {result.returncode})")

    # Count how many demos are now in input/
    dem_files = list(_INPUT_DIR.glob("*.dem"))
    logger.info(f"input/ now contains {len(dem_files)} .dem file(s)")
    return len(dem_files)


@task(name="go-parse", retries=1)
def go_parse() -> list[str]:
    """
    Run the Go parser on all demos in input/.
    Uses --no-clear to append to output/ incrementally.
    Returns list of new base names produced.
    """
    logger = get_run_logger()

    if not _GO_BINARY.exists():
        raise FileNotFoundError(
            f"Go binary not found at {_GO_BINARY}. "
            f"Run: cd {_ROOT} && go build -o dem-parser ."
        )

    existing = set(glob.glob(str(_OUTPUT_DIR / "*_map_activities.csv")))

    logger.info(f"Running Go parser on {_INPUT_DIR} ...")
    result = subprocess.run(
        [str(_GO_BINARY),
         "--input", str(_INPUT_DIR),
         "--output", str(_OUTPUT_DIR),
         "--no-clear"],
        capture_output=True, text=True, cwd=str(_ROOT)
    )
    logger.info(result.stdout[-3000:])  # last 3 KB of output
    if result.returncode != 0:
        logger.error(result.stderr)
        raise RuntimeError(f"Go parser failed (exit {result.returncode})")

    new_files = set(glob.glob(str(_OUTPUT_DIR / "*_map_activities.csv"))) - existing
    new_bases = [Path(f).stem.replace("_map_activities", "") for f in new_files]
    logger.info(f"Parsed {len(new_bases)} new demo(s): {new_bases}")
    return new_bases


@task(name="build-graph-cache")
def build_graph_cache(new_bases: list[str]) -> int:
    """Run build_graphs.py --incremental to cache new demos as .pt files."""
    logger = get_run_logger()
    if not new_bases:
        logger.info("No new demos to cache.")
        return 0

    _CACHE_DIR.mkdir(parents=True, exist_ok=True)
    logger.info(f"Building graph cache for {len(new_bases)} demo(s) ...")

    result = subprocess.run(
        [PYTHON, str(_GNN_DIR / "build_graphs.py"),
         str(_OUTPUT_DIR),
         "--incremental",
         "--cache-dir", str(_CACHE_DIR)],
        capture_output=True, text=True, cwd=str(_GNN_DIR)
    )
    logger.info(result.stdout)
    if result.returncode != 0:
        logger.error(result.stderr)
        raise RuntimeError(f"build_graphs.py failed (exit {result.returncode})")

    cached = list(_CACHE_DIR.glob("*.pt"))
    logger.info(f"Cache now holds {len(cached)} demo graph file(s)")
    return len(cached)


@task(name="delete-csvs")
def delete_csvs(new_bases: list[str]):
    """Delete CSV files for newly-processed demos to reclaim disk space."""
    logger = get_run_logger()
    deleted = 0
    for base in new_bases:
        for pattern in ["*_map_activities.csv", "*_round_tactics.csv",
                        "*_game_events.csv", "*_tactical_events.csv",
                        "*_round_summaries.csv"]:
            for f in _OUTPUT_DIR.glob(base + pattern.lstrip("*")):
                f.unlink(missing_ok=True)
                deleted += 1
    logger.info(f"Deleted {deleted} CSV file(s) for {len(new_bases)} demo(s)")


@task(name="retrain-gnn", timeout_seconds=7200)
def retrain_gnn():
    """Retrain per-map GNN models using all cached .pt graph files."""
    logger = get_run_logger()
    _MODELS_DIR.mkdir(parents=True, exist_ok=True)

    logger.info("Starting GNN training (per-map, eval mode) ...")
    result = subprocess.run(
        [PYTHON, str(_GNN_DIR / "train.py"),
         "--graphs-dir", str(_CACHE_DIR)],
        capture_output=True, text=True, cwd=str(_GNN_DIR)
    )
    logger.info(result.stdout[-5000:])
    if result.returncode != 0:
        logger.error(result.stderr)
        raise RuntimeError(f"train.py failed (exit {result.returncode})")

    # Version and archive produced model checkpoints
    import shutil, time
    version = time.strftime("%Y%m%d_%H%M%S")
    for model_file in (_GNN_DIR / "data").glob("best_model_*.pt"):
        dest = _MODELS_DIR / f"{model_file.stem}_v{version}.pt"
        shutil.copy2(model_file, dest)
        logger.info(f"Archived model: {dest.name}")


@task(name="evaluate-models")
def evaluate_models():
    """Run evaluate.py on all current best_model_*.pt files."""
    logger = get_run_logger()
    eval_script = _GNN_DIR / "evaluate.py"
    if not eval_script.exists():
        logger.warning("evaluate.py not found — skipping evaluation step.")
        return

    result = subprocess.run(
        [PYTHON, str(eval_script)],
        capture_output=True, text=True, cwd=str(_GNN_DIR)
    )
    logger.info(result.stdout[-3000:])
    if result.returncode != 0:
        logger.warning(f"evaluate.py exited {result.returncode}: {result.stderr[-500:]}")


# ── Flow ───────────────────────────────────────────────────────────────────────

@flow(name="cs2-tactic-pipeline",
      description="Download → Parse → Graph → Train CS2 tactic prediction pipeline")
def pipeline(max_demos: int = 50):
    logger = get_run_logger()
    logger.info(f"Pipeline started. max_demos={max_demos}")

    # Stage 1: Download
    total_demos = download_demos(max_demos)

    # Stage 2: Parse
    new_bases = go_parse()

    if not new_bases:
        logger.info("No new demos parsed — nothing more to do.")
        return

    # Stage 3: Graph cache
    cached_count = build_graph_cache(new_bases)

    # Stage 4: Delete CSVs
    delete_csvs(new_bases)

    # Stage 5: Conditional retrain
    if cached_count >= RETRAIN_THRESHOLD or len(new_bases) >= RETRAIN_THRESHOLD:
        logger.info(f"{cached_count} cached demos >= threshold {RETRAIN_THRESHOLD} → retraining")
        retrain_gnn()
        evaluate_models()
    else:
        logger.info(
            f"Only {cached_count} cached demos (threshold {RETRAIN_THRESHOLD}) "
            "— skipping retrain until more data accumulates."
        )

    logger.info("Pipeline complete.")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="CS2 tactic prediction pipeline")
    parser.add_argument("--max-demos", type=int, default=50,
                        help="Max new demos to download per run (default: 50)")
    parser.add_argument("--deploy", action="store_true",
                        help="Deploy as a scheduled Prefect flow (runs daily at 02:00 UTC)")
    parser.add_argument("--force-retrain", action="store_true",
                        help="Skip download/parse and immediately retrain from cache")
    args = parser.parse_args()

    if args.force_retrain:
        print("Force retrain from cache ...")
        retrain_gnn()
        evaluate_models()
    elif args.deploy:
        from prefect.deployments import Deployment
        deployment = Deployment.build_from_flow(
            flow=pipeline,
            name="daily-cs2-pipeline",
            schedule=CronSchedule(cron="0 2 * * *", timezone="UTC"),
            parameters={"max_demos": args.max_demos},
        )
        deployment.apply()
        print("Deployment registered. Start with: prefect agent start -q default")
    else:
        pipeline(max_demos=args.max_demos)
