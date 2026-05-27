"""
HLTV demo downloader for the CS2 tactic prediction pipeline.

Downloads professional CS2/CS:GO match demos from HLTV.org with rate limiting.
Tracks already-downloaded match IDs in downloaded.txt to support incremental runs.

Filters applied:
  - Professional tier only (HLTV Tier 1/2 events)
  - CS:GO matches >= 2015, CS2 matches >= September 2023
  - Active-Duty maps only: Anubis, Ancient, Dust2, Inferno, Mirage, Nuke, Overpass

Usage:
  python3 pipeline/download.py --limit 10
  python3 pipeline/download.py --limit 50 --output input/

Requires:
  pip install requests beautifulsoup4 tqdm
"""

import argparse
import os
import re
import time
import zipfile
from pathlib import Path

import requests
from bs4 import BeautifulSoup
from tqdm import tqdm

# Active-Duty map pool (Jan 2026). Demos on other maps are skipped.
ACTIVE_DUTY_MAPS = {
    "anubis", "ancient", "dust2", "inferno", "mirage", "nuke", "overpass"
}

# HLTV-reported map name variants → canonical name
MAP_ALIASES = {
    "de_anubis": "anubis",
    "de_ancient": "ancient",
    "de_dust2": "dust2",
    "de_inferno": "inferno",
    "de_mirage": "mirage",
    "de_nuke": "nuke",
    "de_overpass": "overpass",
}

HLTV_BASE = "https://www.hltv.org"
# Minimum wait between HTTP requests (seconds) — polite scraping.
REQUEST_DELAY = 3.0

HEADERS = {
    "User-Agent": (
        "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 "
        "(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
    ),
    "Accept-Language": "en-US,en;q=0.9",
}


def _get(url: str, session: requests.Session) -> requests.Response:
    time.sleep(REQUEST_DELAY)
    resp = session.get(url, headers=HEADERS, timeout=30)
    resp.raise_for_status()
    return resp


def load_downloaded(state_file: str) -> set:
    if not os.path.exists(state_file):
        return set()
    with open(state_file) as f:
        return {line.strip() for line in f if line.strip()}


def save_downloaded(match_id: str, state_file: str):
    with open(state_file, "a") as f:
        f.write(match_id + "\n")


def is_active_duty(map_name: str) -> bool:
    """Return True if map_name (any variant) is in the Active-Duty pool."""
    canonical = MAP_ALIASES.get(map_name.lower(), map_name.lower().replace("de_", ""))
    return canonical in ACTIVE_DUTY_MAPS


def fetch_match_ids(session: requests.Session, offset: int = 0,
                    limit: int = 50) -> list[dict]:
    """
    Scrape match listing page from HLTV for professional tier events.
    Returns list of dicts with {match_id, map, url}.

    Note: HLTV blocks automated scraping aggressively. In production:
      - Use a rotating residential proxy pool, or
      - Use the ESTA dataset (1,558 matches CC BY-SA 4.0) for bulk historical data.
    """
    url = f"{HLTV_BASE}/results?offset={offset}&stars=1"
    try:
        resp = _get(url, session)
    except requests.HTTPError as e:
        print(f"  HTTP {e.response.status_code} fetching match list — HLTV may be blocking.")
        return []

    soup = BeautifulSoup(resp.text, "html.parser")
    matches = []

    for row in soup.select(".result-con a.a-reset"):
        href = row.get("href", "")
        m = re.search(r"/matches/(\d+)/", href)
        if not m:
            continue
        match_id = m.group(1)

        # Extract map from row
        map_cell = row.select_one(".map-text-cell") or row.select_one(".gtSmartphone-only")
        map_name = map_cell.get_text(strip=True) if map_cell else ""

        if not is_active_duty(map_name):
            continue

        matches.append({"match_id": match_id, "map": map_name,
                        "url": HLTV_BASE + href})
        if len(matches) >= limit:
            break

    return matches


def get_demo_download_url(match_url: str, session: requests.Session) -> str | None:
    """Parse match page and return the .dem download URL, or None if not found."""
    try:
        resp = _get(match_url, session)
    except requests.HTTPError:
        return None

    soup = BeautifulSoup(resp.text, "html.parser")
    demo_link = soup.select_one("a[href*='/download/demo/']")
    if demo_link:
        return HLTV_BASE + demo_link["href"]
    return None


def download_demo(download_url: str, dest_dir: str,
                  match_id: str, session: requests.Session) -> str | None:
    """
    Download a demo ZIP and extract .dem files to dest_dir.
    Returns the path to the extracted .dem file, or None on failure.
    """
    zip_path = os.path.join(dest_dir, f"{match_id}.zip")
    try:
        resp = session.get(download_url, headers=HEADERS,
                           stream=True, timeout=120)
        resp.raise_for_status()
        total = int(resp.headers.get("content-length", 0))
        with open(zip_path, "wb") as f, tqdm(
                total=total, unit="B", unit_scale=True,
                desc=f"  match {match_id}", leave=False) as bar:
            for chunk in resp.iter_content(chunk_size=8192):
                f.write(chunk)
                bar.update(len(chunk))
    except Exception as e:
        print(f"  Download failed for match {match_id}: {e}")
        if os.path.exists(zip_path):
            os.remove(zip_path)
        return None

    # Extract .dem from zip
    dem_path = None
    try:
        with zipfile.ZipFile(zip_path, "r") as zf:
            for name in zf.namelist():
                if name.endswith(".dem"):
                    zf.extract(name, dest_dir)
                    dem_path = os.path.join(dest_dir, name)
                    # Rename to match_id.dem for consistent naming
                    final = os.path.join(dest_dir, f"{match_id}.dem")
                    os.rename(dem_path, final)
                    dem_path = final
                    break
    except zipfile.BadZipFile:
        print(f"  Bad ZIP for match {match_id}")
    finally:
        os.remove(zip_path)

    return dem_path


def run(limit: int, output_dir: str, state_file: str):
    os.makedirs(output_dir, exist_ok=True)
    downloaded = load_downloaded(state_file)
    print(f"Already downloaded: {len(downloaded)} matches")
    print(f"Target: {limit} new demos → {output_dir}\n")

    session = requests.Session()
    new_count = 0
    offset = 0

    while new_count < limit:
        batch = fetch_match_ids(session, offset=offset, limit=50)
        if not batch:
            print("No more matches found or HLTV is blocking — stopping.")
            break

        for match in batch:
            match_id = match["match_id"]
            if match_id in downloaded:
                continue
            if new_count >= limit:
                break

            print(f"Match {match_id} ({match['map']}) ...")
            demo_url = get_demo_download_url(match["url"], session)
            if not demo_url:
                print(f"  No demo link found, skipping.")
                continue

            dem_path = download_demo(demo_url, output_dir, match_id, session)
            if dem_path:
                save_downloaded(match_id, state_file)
                downloaded.add(match_id)
                new_count += 1
                print(f"  Saved: {dem_path}  [{new_count}/{limit}]")

        offset += 50
        if len(batch) < 50:
            break

    print(f"\nDone. Downloaded {new_count} new demo(s).")


if __name__ == "__main__":
    _here = Path(__file__).parent
    parser = argparse.ArgumentParser(description="Download HLTV professional demos")
    parser.add_argument("--limit", type=int, default=10,
                        help="Number of new demos to download (default: 10)")
    parser.add_argument("--output", default=str(_here.parent / "input"),
                        help="Destination directory for .dem files (default: input/)")
    parser.add_argument("--state", default=str(_here / "downloaded.txt"),
                        help="File tracking downloaded match IDs")
    args = parser.parse_args()
    run(limit=args.limit, output_dir=args.output, state_file=args.state)
