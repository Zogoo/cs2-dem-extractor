"""
Build PyTorch Geometric graph objects from dem-parser CSV outputs.

Each graph = one snapshot (Round, Tick) containing:
  - 10 player nodes (5T + 5CT, padded with zeros if dead/missing)
  - 2 bombsite nodes (A and B, fixed)
  - 1 bomb node (C4 carrier position or planted location)

Node features (per node):
  - X, Y, Z normalized per-map to [0, 1] using actual map bounds
  - Health / 100
  - Armor / 100
  - isAlive (binary)
  - hasBomb (binary)
  - ViewDirectionX / 360 (yaw, normalized)
  - ViewDirectionY / 90  (pitch, normalized — critical for Nuke upper/lower)
  - TacticalZone one-hot (7 zones + Unknown = 8)
  - NodeType one-hot (player / bombsite / bomb = 3)
  - Team one-hot (T / CT / none = 3)
  - Distance to A-site centroid (normalized by map diagonal)
  - Distance to B-site centroid (normalized by map diagonal)
Total: 3 + 1 + 1 + 1 + 1 + 1 + 1 + 8 + 3 + 3 + 1 + 1 = 25 features per node

Graph-level label: tactic class from RoundTactic column (T-side only).
Labels are kept granular (e.g. Fake_A_to_B, Fake_B_to_A) per map.
Classes with < MIN_SAMPLES_PER_MAP samples are merged into "Other".

Snapshots at 20s, 30s, 40s per round (10s dropped — players still in spawn).
"""

import os
import glob
import pandas as pd
import numpy as np
import torch
from collections import Counter
from torch_geometric.data import Data

TACTICAL_ZONES = [
    "T_SPAWN", "A_APPROACH", "A_SITE", "B_APPROACH",
    "B_SITE", "MID", "CT_SPAWN", "Unknown"
]
ZONE_TO_IDX = {z: i for i, z in enumerate(TACTICAL_ZONES)}

# Minimum samples for a tactic class to get its own label per map.
# Classes below this threshold are merged into "Other".
MIN_SAMPLES_PER_MAP = 2

NUM_NODE_FEATURES = 25  # 3+1+1+1+1+1+1+8+3+3+1+1 (added Z, ViewDirectionY)


# ── Map stats ──────────────────────────────────────────────────────────────

def compute_map_stats(df: pd.DataFrame) -> dict:
    """
    Compute per-map coordinate bounds and bombsite centroids from player data.
    Includes Z axis — critical for Nuke (upper/lower) and other vertical maps.
    """
    x_min, x_max = float(df["X"].min()), float(df["X"].max())
    y_min, y_max = float(df["Y"].min()), float(df["Y"].max())
    z_min, z_max = (float(df["Z"].min()), float(df["Z"].max())) if "Z" in df.columns else (0.0, 1.0)
    x_range = (x_max - x_min) or 1.0
    y_range = (y_max - y_min) or 1.0
    z_range = (z_max - z_min) or 1.0
    diag = (x_range ** 2 + y_range ** 2) ** 0.5

    a_rows = df[df["TacticalZone"] == "A_SITE"]
    b_rows = df[df["TacticalZone"] == "B_SITE"]

    a_cx = float(a_rows["X"].mean()) if len(a_rows) > 0 else (x_min + x_max) / 2
    a_cy = float(a_rows["Y"].mean()) if len(a_rows) > 0 else (y_min + y_max) / 2
    b_cx = float(b_rows["X"].mean()) if len(b_rows) > 0 else (x_min + x_max) / 2
    b_cy = float(b_rows["Y"].mean()) if len(b_rows) > 0 else (y_min + y_max) / 2

    return dict(x_min=x_min, x_max=x_max, x_range=x_range,
                y_min=y_min, y_max=y_max, y_range=y_range,
                z_min=z_min, z_max=z_max, z_range=z_range,
                a_cx=a_cx, a_cy=a_cy, b_cx=b_cx, b_cy=b_cy, diag=diag)


def _dist(x, y, cx, cy, diag):
    return ((x - cx) ** 2 + (y - cy) ** 2) ** 0.5 / diag


# ── Label helpers ──────────────────────────────────────────────────────────

def get_map_classes(tactics_df: pd.DataFrame) -> list:
    """
    Return sorted tactic class list for this map.
    Classes with fewer than MIN_SAMPLES_PER_MAP rounds are collapsed to 'Other'.
    """
    counts = Counter(tactics_df["T_TacticLabel"].dropna().tolist())
    classes = sorted(label for label, n in counts.items() if n >= MIN_SAMPLES_PER_MAP)
    if any(n < MIN_SAMPLES_PER_MAP for n in counts.values()):
        classes.append("Other")
    return classes


def resolve_label(raw_label: str, valid_classes: list) -> str:
    """Map a raw tactic label to a valid class, or 'Other'."""
    if not raw_label or pd.isna(raw_label):
        return None
    if raw_label in valid_classes:
        return raw_label
    return "Other" if "Other" in valid_classes else None


# ── Feature builders ───────────────────────────────────────────────────────

def one_hot_zone(zone: str) -> list:
    vec = [0.0] * len(TACTICAL_ZONES)
    vec[ZONE_TO_IDX.get(zone, ZONE_TO_IDX["Unknown"])] = 1.0
    return vec


def one_hot_node_type(ntype: str) -> list:
    vec = [0.0, 0.0, 0.0]
    if ntype == "player":   vec[0] = 1.0
    elif ntype == "bombsite": vec[1] = 1.0
    elif ntype == "bomb":   vec[2] = 1.0
    return vec


def one_hot_team(team: str) -> list:
    vec = [0.0, 0.0, 0.0]
    if team == "T":    vec[0] = 1.0
    elif team == "CT": vec[1] = 1.0
    else:              vec[2] = 1.0
    return vec


def build_player_features(row, stats: dict) -> list:
    x, y = float(row["X"]), float(row["Y"])
    z = float(row.get("Z", 0.0)) if "Z" in row.index else 0.0
    x_n = (x - stats["x_min"]) / stats["x_range"]
    y_n = (y - stats["y_min"]) / stats["y_range"]
    z_n = (z - stats["z_min"]) / stats["z_range"]
    vx = float(row.get("ViewDirectionX", 0.0)) if "ViewDirectionX" in row.index else 0.0
    vy = float(row.get("ViewDirectionY", 0.0)) if "ViewDirectionY" in row.index else 0.0
    return [
        x_n, y_n, z_n,
        float(row["Health"]) / 100.0,
        float(row["Armor"]) / 100.0,
        1.0 if row["IsAlive"] else 0.0,
        1.0 if row["HasC4"] else 0.0,
        vx / 360.0,
        vy / 90.0,   # pitch in [-90, 90]
        *one_hot_zone(row["TacticalZone"]),
        *one_hot_node_type("player"),
        *one_hot_team(row["PlayerTeam"]),
        _dist(x, y, stats["a_cx"], stats["a_cy"], stats["diag"]),
        _dist(x, y, stats["b_cx"], stats["b_cy"], stats["diag"]),
    ]


def build_dead_player_features(team: str, stats: dict) -> list:
    cx = (stats["x_min"] + stats["x_max"]) / 2
    cy = (stats["y_min"] + stats["y_max"]) / 2
    return [
        0.5, 0.5, 0.5,          # map center normalized XYZ
        0.0, 0.0,               # health, armor
        0.0, 0.0,               # alive, hasBomb
        0.0, 0.0,               # viewX, viewY
        *one_hot_zone("Unknown"),
        *one_hot_node_type("player"),
        *one_hot_team(team),
        _dist(cx, cy, stats["a_cx"], stats["a_cy"], stats["diag"]),
        _dist(cx, cy, stats["b_cx"], stats["b_cy"], stats["diag"]),
    ]


def build_bombsite_features(zone: str, stats: dict) -> list:
    cx = stats["a_cx"] if zone == "A_SITE" else stats["b_cx"]
    cy = stats["a_cy"] if zone == "A_SITE" else stats["b_cy"]
    x_n = (cx - stats["x_min"]) / stats["x_range"]
    y_n = (cy - stats["y_min"]) / stats["y_range"]
    return [
        x_n, y_n, 0.5,          # XYZ (Z unknown for abstract bombsite node)
        0.0, 0.0, 1.0, 0.0,    # health, armor, alive, hasBomb
        0.0, 0.0,               # viewX, viewY
        *one_hot_zone(zone),
        *one_hot_node_type("bombsite"),
        *one_hot_team("none"),
        _dist(cx, cy, stats["a_cx"], stats["a_cy"], stats["diag"]),
        _dist(cx, cy, stats["b_cx"], stats["b_cy"], stats["diag"]),
    ]


def build_bomb_features(x, y, zone: str, stats: dict) -> list:
    x_n = (float(x) - stats["x_min"]) / stats["x_range"]
    y_n = (float(y) - stats["y_min"]) / stats["y_range"]
    return [
        x_n, y_n, 0.5,          # XYZ (bomb Z unknown without separate tracking)
        0.0, 0.0, 1.0, 1.0,    # health, armor, alive, hasBomb
        0.0, 0.0,               # viewX, viewY
        *one_hot_zone(zone),
        *one_hot_node_type("bomb"),
        *one_hot_team("none"),
        _dist(float(x), float(y), stats["a_cx"], stats["a_cy"], stats["diag"]),
        _dist(float(x), float(y), stats["b_cx"], stats["b_cy"], stats["diag"]),
    ]


# ── Graph builder ──────────────────────────────────────────────────────────

def build_graph(tick_df: pd.DataFrame, tactic_label: str,
                tactic_classes: list, tactic_to_idx: dict,
                stats: dict, round_num: int,
                map_name: str, snapshot_time: float) -> Data:
    """Build a single graph from all player rows at one (Round, Tick)."""
    cls = resolve_label(tactic_label, tactic_classes)
    if cls is None:
        return None
    class_idx = tactic_to_idx[cls]

    nodes = []

    t_players = (tick_df[tick_df["PlayerTeam"] == "T"]
                 .drop_duplicates(subset="PlayerName", keep="first")
                 .head(5))
    ct_players = (tick_df[tick_df["PlayerTeam"] == "CT"]
                  .drop_duplicates(subset="PlayerName", keep="first")
                  .head(5))

    for _, row in t_players.iterrows():
        nodes.append(build_player_features(row, stats))
    for _ in range(5 - len(t_players)):
        nodes.append(build_dead_player_features("T", stats))

    for _, row in ct_players.iterrows():
        nodes.append(build_player_features(row, stats))
    for _ in range(5 - len(ct_players)):
        nodes.append(build_dead_player_features("CT", stats))

    nodes.append(build_bombsite_features("A_SITE", stats))
    nodes.append(build_bombsite_features("B_SITE", stats))

    c4_rows = tick_df[tick_df["HasC4"] == True]
    if len(c4_rows) > 0:
        c4 = c4_rows.iloc[0]
        bomb_x, bomb_y, bomb_zone = c4["X"], c4["Y"], c4["TacticalZone"]
    else:
        bomb_x, bomb_y, bomb_zone = 0.0, 0.0, "Unknown"
    nodes.append(build_bomb_features(bomb_x, bomb_y, bomb_zone, stats))

    x = torch.tensor(nodes, dtype=torch.float)
    assert x.shape == (13, NUM_NODE_FEATURES), f"Shape mismatch: {x.shape}"

    # Fully connected directed edges between all pairs
    n = 13
    src = [i for i in range(n) for j in range(n) if i != j]
    dst = [j for i in range(n) for j in range(n) if i != j]
    edge_index = torch.tensor([src, dst], dtype=torch.long)

    data = Data(x=x, edge_index=edge_index,
                y=torch.tensor([class_idx], dtype=torch.long))
    data.round_num = round_num
    data.map_name = map_name
    data.snapshot_time = snapshot_time
    data.tactic_detail = tactic_label
    data.tactic_class = cls
    return data


# ── Main loader ────────────────────────────────────────────────────────────

def load_and_build_graphs(output_dir: str,
                          snapshot_offsets=(20.0, 30.0, 40.0)) -> list:
    """
    Build graphs for ALL maps combined, using broad label mapping.
    Useful for cross-map experiments. For best accuracy, use
    load_graphs_per_map() which trains per-map with granular labels.
    """
    activity_files = sorted(glob.glob(
        os.path.join(output_dir, "*_map_activities.csv")))
    if not activity_files:
        print(f"No *_map_activities.csv files found in {output_dir}")
        return []

    all_graphs = []
    for act_file in activity_files:
        base = os.path.basename(act_file).replace("_map_activities.csv", "")
        tactic_file = os.path.join(output_dir, base + "_round_tactics.csv")
        if not os.path.exists(tactic_file):
            print(f"Skipping {base}: no round_tactics.csv")
            continue

        print(f"Loading {base}...")
        df = pd.read_csv(act_file)
        tactics_df = pd.read_csv(tactic_file)
        _parse_bools(df)

        stats = compute_map_stats(df)
        tactic_classes = get_map_classes(tactics_df)
        tactic_to_idx = {c: i for i, c in enumerate(tactic_classes)}
        tactic_map = dict(zip(tactics_df["Round"], tactics_df["T_TacticLabel"]))
        map_name = base.split("-")[-1] if "-" in base else "unknown"

        graphs = _build_map_graphs(df, tactic_map, tactic_classes,
                                   tactic_to_idx, stats, map_name,
                                   snapshot_offsets)
        all_graphs.extend(graphs)
        print(f"  {len(graphs)} graphs from {base}")

    print(f"\nTotal graphs: {len(all_graphs)}")
    return all_graphs


def load_graphs_per_map(output_dir: str,
                        snapshot_offsets=(20.0, 30.0, 40.0)) -> dict:
    """
    Build graphs separately per map, with per-map granular label sets.
    Returns dict: {map_name: {'graphs': [...], 'classes': [...], 'stats': {...}}}
    This is the recommended path for maximum accuracy.
    """
    activity_files = sorted(glob.glob(
        os.path.join(output_dir, "*_map_activities.csv")))
    if not activity_files:
        print(f"No *_map_activities.csv files found in {output_dir}")
        return {}

    result = {}
    for act_file in activity_files:
        base = os.path.basename(act_file).replace("_map_activities.csv", "")
        tactic_file = os.path.join(output_dir, base + "_round_tactics.csv")
        if not os.path.exists(tactic_file):
            print(f"Skipping {base}: no round_tactics.csv")
            continue

        print(f"Loading {base}...")
        df = pd.read_csv(act_file)
        tactics_df = pd.read_csv(tactic_file)
        _parse_bools(df)

        stats = compute_map_stats(df)
        tactic_classes = get_map_classes(tactics_df)
        tactic_to_idx = {c: i for i, c in enumerate(tactic_classes)}
        tactic_map = dict(zip(tactics_df["Round"], tactics_df["T_TacticLabel"]))
        map_name = base.split("-")[-1] if "-" in base else base

        graphs = _build_map_graphs(df, tactic_map, tactic_classes,
                                   tactic_to_idx, stats, map_name,
                                   snapshot_offsets)

        print(f"  {len(graphs)} graphs | classes: {tactic_classes}")
        dist = Counter(g.tactic_class for g in graphs)
        for cls, n in sorted(dist.items(), key=lambda x: -x[1]):
            print(f"    {cls}: {n}")

        result[map_name] = {
            "graphs": graphs,
            "classes": tactic_classes,
            "stats": stats,
            "base": base,
        }

    return result


# ── Internal helpers ───────────────────────────────────────────────────────

def _parse_bools(df: pd.DataFrame):
    for col in ["IsAlive", "HasC4", "IsInBombZone", "IsInBuyZone"]:
        if col in df.columns:
            df[col] = df[col].map(
                {"true": True, "false": False, True: True, False: False})


def _build_map_graphs(df, tactic_map, tactic_classes, tactic_to_idx,
                      stats, map_name, snapshot_offsets) -> list:
    graphs = []
    round_start = df.groupby("Round")["Time"].min().to_dict()

    for round_num, round_df in df.groupby("Round"):
        if round_num not in tactic_map:
            continue
        raw_label = tactic_map[round_num]
        start_time = round_start.get(round_num, 0)
        alive_df = round_df[round_df["IsAlive"] == True]
        if len(alive_df) == 0:
            continue

        tick_times = alive_df.groupby("Tick")["Time"].first()
        if len(tick_times) == 0:
            continue

        for offset in snapshot_offsets:
            target = start_time + offset
            best_tick = tick_times.index[
                (tick_times - target).abs().argmin()]
            tick_df = alive_df[alive_df["Tick"] == best_tick]
            if len(tick_df) == 0:
                continue

            g = build_graph(tick_df, raw_label, tactic_classes,
                            tactic_to_idx, stats, round_num,
                            map_name, offset)
            if g is not None:
                graphs.append(g)
    return graphs


# ── Persistence ────────────────────────────────────────────────────────────

def save_graphs(graphs: list, output_path: str):
    os.makedirs(os.path.dirname(output_path) or ".", exist_ok=True)
    torch.save(graphs, output_path)
    print(f"Saved {len(graphs)} graphs to {output_path}")


def load_graphs(path: str) -> list:
    graphs = torch.load(path, weights_only=False)
    print(f"Loaded {len(graphs)} graphs from {path}")
    return graphs


if __name__ == "__main__":
    import sys
    output_dir = sys.argv[1] if len(sys.argv) > 1 else os.path.join(
        os.path.dirname(__file__), "..", "output")
    save_path = os.path.join(os.path.dirname(__file__), "data", "graphs.pt")

    print(f"Building graphs from: {output_dir}")
    graphs = load_and_build_graphs(output_dir)

    if graphs:
        labels_all = [g.tactic_class for g in graphs]
        print("\nClass distribution:")
        for cls, n in Counter(labels_all).most_common():
            print(f"  {cls}: {n}")
        save_graphs(graphs, save_path)
