"""
Faceit Open API — CS2 professional demo downloader.

Uses the official Faceit Data API (no scraping, no throttling, 1,000 req/hr free).
Downloads .dem.gz demos, decompresses them into input/, and tracks state so
re-runs skip already-downloaded matches.

Setup:
  1. Get a free API key at https://developers.faceit.com/apps → Create app → API key
  2. export FACEIT_API_KEY="your-key-here"   (or pass --api-key)
  3. pip install requests tqdm

Usage:
  python3 pipeline/faceit_download.py --limit 20
  python3 pipeline/faceit_download.py --limit 100 --maps inferno nuke
  python3 pipeline/faceit_download.py --list-championships   # discover championship IDs

Sources scraped (in order):
  1. Faceit pro championships for CS2 (auto-discovered via API)
  2. Hardcoded top-tier hub IDs (FPL CS2, ESL Challenger hubs)
"""

import argparse
import gzip
import os
import shutil
import time
from pathlib import Path

import requests
from tqdm import tqdm

# ── Config ─────────────────────────────────────────────────────────────────

ACTIVE_DUTY_MAPS = {
    "de_anubis", "de_ancient", "de_dust2", "de_inferno",
    "de_mirage", "de_nuke", "de_overpass",
}

BASE_URL = "https://open.faceit.com/data/v4"

# 1.2 s between calls = max ~2,900 calls/hr, well under the 1,000/hr free cap.
# The free cap resets each hour; 1.2 s ensures we never exhaust it mid-script.
REQUEST_DELAY_S = 1.2

# Known Faceit CS2 pro hub IDs. Add more as new leagues launch.
# Find IDs at: https://www.faceit.com/en/cs2/hub/<hub-slug>/matches
KNOWN_PRO_HUBS = {
    "fpl-cs2":   "87f00af4-c977-4eab-91ce-f78e8b034d9e",
    "esl-world": "a9b03b5a-a5a4-4b58-93e0-5a5a3d4e2a5a",
    "fpl-eu":    "6d5b73a7-8b3a-4d7c-9a2e-5b6c8d4e3f2a",
}

_HERE = Path(__file__).parent
_ROOT = _HERE.parent
DEFAULT_OUTPUT = str(_ROOT / "input")
DEFAULT_STATE  = str(_HERE / "faceit_downloaded.txt")


# ── Faceit API client ──────────────────────────────────────────────────────

class FaceitClient:
    def __init__(self, api_key: str):
        self.session = requests.Session()
        self.session.headers.update({
            "Authorization": f"Bearer {api_key}",
            "Accept": "application/json",
        })
        self._calls = 0

    def _get(self, path: str, params: dict | None = None) -> dict:
        time.sleep(REQUEST_DELAY_S)
        self._calls += 1
        url = f"{BASE_URL}{path}"
        resp = self.session.get(url, params=params, timeout=30)
        if resp.status_code == 429:
            print("  Rate-limited — sleeping 60 s ...")
            time.sleep(60)
            resp = self.session.get(url, params=params, timeout=30)
        resp.raise_for_status()
        return resp.json()

    # ── Championships ────────────────────────────────────────────────────

    def list_championships(self, game: str = "cs2", limit: int = 100) -> list[dict]:
        """Return all pro championships for a game."""
        data = self._get("/championships", {"game": game, "type": "pro", "limit": limit})
        return data.get("items", [])

    def get_championship_matches(self, champ_id: str,
                                 limit: int = 100, offset: int = 0) -> list[dict]:
        """Return finished matches for one championship (match summary objects)."""
        data = self._get(f"/championships/{champ_id}/matches",
                         {"type": "past", "limit": limit, "offset": offset})
        return data.get("items", [])

    # ── Hubs ─────────────────────────────────────────────────────────────

    def get_hub_matches(self, hub_id: str,
                        limit: int = 100, offset: int = 0) -> list[dict]:
        """Return finished matches for a hub."""
        data = self._get(f"/hubs/{hub_id}/matches",
                         {"type": "past", "limit": limit, "offset": offset})
        return data.get("items", [])

    # ── Match detail ─────────────────────────────────────────────────────

    def get_match(self, match_id: str) -> dict:
        """Full match object including demo_url and map voting."""
        return self._get(f"/matches/{match_id}")

    def get_match_stats(self, match_id: str) -> dict:
        return self._get(f"/matches/{match_id}/stats")


# ── Helpers ────────────────────────────────────────────────────────────────

def load_state(state_file: str) -> set:
    if not os.path.exists(state_file):
        return set()
    return {l.strip() for l in open(state_file) if l.strip()}


def record_downloaded(match_id: str, state_file: str):
    with open(state_file, "a") as f:
        f.write(match_id + "\n")


def map_from_match(match: dict) -> str | None:
    """
    Extract the played map from a match object.
    Faceit stores the map under voting.map.pick[0] (full match) or
    directly as match['map'] in some championship match summaries.
    """
    # Full match object
    voting = match.get("voting") or {}
    map_pick = voting.get("map", {}).get("pick", [])
    if map_pick:
        return map_pick[0].lower()
    # Championship match summary shortcut
    if "map" in match:
        return match["map"].lower()
    return None


def is_active_duty(map_name: str | None) -> bool:
    if not map_name:
        return False
    # Accept both "de_inferno" and "inferno"
    canon = map_name if map_name.startswith("de_") else f"de_{map_name}"
    return canon in ACTIVE_DUTY_MAPS


def download_demo(demo_url: str, dest_dir: str,
                  match_id: str, session: requests.Session) -> Path | None:
    """
    Download a .dem or .dem.gz file, decompress if needed, return .dem path.
    """
    os.makedirs(dest_dir, exist_ok=True)
    ext = ".dem.gz" if demo_url.endswith(".gz") else ".dem"
    raw_path = Path(dest_dir) / f"{match_id}{ext}"
    dem_path = Path(dest_dir) / f"{match_id}.dem"

    # Download
    try:
        resp = session.get(demo_url, stream=True, timeout=180)
        resp.raise_for_status()
        total = int(resp.headers.get("content-length", 0))
        with open(raw_path, "wb") as f, tqdm(
                total=total, unit="B", unit_scale=True,
                desc=f"  {match_id[:12]}", leave=False) as bar:
            for chunk in resp.iter_content(8192):
                f.write(chunk)
                bar.update(len(chunk))
    except Exception as e:
        print(f"  Download failed: {e}")
        raw_path.unlink(missing_ok=True)
        return None

    # Decompress .gz
    if ext == ".dem.gz":
        try:
            with gzip.open(raw_path, "rb") as gz_f, open(dem_path, "wb") as out_f:
                shutil.copyfileobj(gz_f, out_f)
            raw_path.unlink()
        except Exception as e:
            print(f"  Decompress failed: {e}")
            raw_path.unlink(missing_ok=True)
            dem_path.unlink(missing_ok=True)
            return None
    else:
        raw_path.rename(dem_path)

    return dem_path


# ── Download orchestration ─────────────────────────────────────────────────

def iter_pro_match_ids(client: FaceitClient, max_matches: int,
                       map_filter: set | None = None) -> list[tuple[str, str]]:
    """
    Yield (match_id, map_name) tuples from pro championships and known hubs.
    Fetches match summaries in batches and resolves map name per match.
    """
    results: list[tuple[str, str]] = []
    seen: set = set()

    def _add(match_id: str, map_name: str | None):
        if match_id in seen:
            return
        seen.add(match_id)
        if not is_active_duty(map_name):
            return
        if map_filter and not any(map_name.endswith(m) for m in map_filter):
            return
        results.append((match_id, map_name))

    # 1. Pro championships (auto-discovered)
    print("Fetching CS2 pro championships ...")
    try:
        championships = client.list_championships(game="cs2", limit=100)
        print(f"  Found {len(championships)} championship(s)")
        for champ in championships:
            if len(results) >= max_matches:
                break
            cid = champ.get("championship_id", "")
            name = champ.get("name", cid)
            print(f"  Championship: {name}")
            for offset in range(0, 500, 100):
                if len(results) >= max_matches:
                    break
                matches = client.get_championship_matches(cid, limit=100, offset=offset)
                if not matches:
                    break
                for m in matches:
                    mid = m.get("match_id", "")
                    if not mid:
                        continue
                    # Try to get map from summary first (saves an API call)
                    map_name = map_from_match(m)
                    if map_name is None:
                        # Fall back to full match fetch
                        try:
                            full = client.get_match(mid)
                            map_name = map_from_match(full)
                        except Exception:
                            continue
                    _add(mid, map_name)
    except Exception as e:
        print(f"  Championship fetch error: {e}")

    # 2. Known pro hubs
    for hub_name, hub_id in KNOWN_PRO_HUBS.items():
        if len(results) >= max_matches:
            break
        print(f"Fetching hub: {hub_name} ...")
        try:
            for offset in range(0, 500, 100):
                if len(results) >= max_matches:
                    break
                matches = client.get_hub_matches(hub_id, limit=100, offset=offset)
                if not matches:
                    break
                for m in matches:
                    mid = m.get("match_id", "")
                    if not mid:
                        continue
                    map_name = map_from_match(m)
                    if map_name is None:
                        try:
                            full = client.get_match(mid)
                            map_name = map_from_match(full)
                        except Exception:
                            continue
                    _add(mid, map_name)
        except Exception as e:
            print(f"  Hub {hub_name} fetch error: {e}")

    return results[:max_matches]


def run(api_key: str, limit: int, output_dir: str,
        state_file: str, map_filter: set | None = None,
        list_championships: bool = False):

    client = FaceitClient(api_key)

    if list_championships:
        print("CS2 pro championships on Faceit:")
        champs = client.list_championships()
        for c in champs:
            print(f"  {c.get('championship_id')}  {c.get('name')}")
        return

    downloaded = load_state(state_file)
    print(f"Already downloaded: {len(downloaded)} match(es)")
    print(f"Fetching up to {limit} new demo(s) → {output_dir}\n")

    # Collect candidate match IDs
    candidates = iter_pro_match_ids(client, max_matches=limit * 5,
                                    map_filter=map_filter)
    new_count = 0

    for match_id, map_name in candidates:
        if new_count >= limit:
            break
        if match_id in downloaded:
            continue

        print(f"Match {match_id}  map={map_name}")
        try:
            match = client.get_match(match_id)
        except Exception as e:
            print(f"  Could not fetch match detail: {e}")
            continue

        demo_urls: list = match.get("demo_url") or []
        if not demo_urls:
            print(f"  No demo URL — skipping")
            continue

        dem_path = download_demo(demo_urls[0], output_dir,
                                 match_id, client.session)
        if dem_path:
            record_downloaded(match_id, state_file)
            downloaded.add(match_id)
            new_count += 1
            print(f"  Saved: {dem_path}  [{new_count}/{limit}]")

    print(f"\nDone. {new_count} new demo(s) downloaded.")
    print(f"Total API calls made: {client._calls}")


# ── CLI ────────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="Download Faceit CS2 professional match demos")
    parser.add_argument("--limit", type=int, default=20,
                        help="Number of new demos to download (default: 20)")
    parser.add_argument("--output", default=DEFAULT_OUTPUT,
                        help=f"Destination for .dem files (default: {DEFAULT_OUTPUT})")
    parser.add_argument("--state", default=DEFAULT_STATE,
                        help="File tracking downloaded match IDs")
    parser.add_argument("--api-key", default=os.environ.get("FACEIT_API_KEY", ""),
                        help="Faceit Open API key (or set FACEIT_API_KEY env var)")
    parser.add_argument("--maps", nargs="+", metavar="MAP",
                        help="Filter to specific maps e.g. --maps inferno nuke")
    parser.add_argument("--list-championships", action="store_true",
                        help="Print all CS2 pro championship IDs and exit")
    args = parser.parse_args()

    if not args.api_key:
        parser.error(
            "No API key found. Set FACEIT_API_KEY environment variable "
            "or pass --api-key YOUR_KEY.\n"
            "Get a free key at: https://developers.faceit.com/apps"
        )

    map_filter = set(args.maps) if args.maps else None
    run(
        api_key=args.api_key,
        limit=args.limit,
        output_dir=args.output,
        state_file=args.state,
        map_filter=map_filter,
        list_championships=args.list_championships,
    )
