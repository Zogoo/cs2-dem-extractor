"""
Build PyTorch Geometric graph objects from dem-parser CSV outputs.

Each graph = one snapshot (Round, Tick) containing:
  - 10 player nodes (5T + 5CT, padded with zeros if dead/missing)
  - 2 bombsite nodes (A and B, fixed)
  - 1 bomb node (C4 carrier position or planted location)

Node features (per node):
  - X, Y normalized to [-1, 1]
  - Health / 100
  - Armor / 100
  - isAlive (binary)
  - hasBomb (binary)
  - ViewDirectionX / 360 (normalized)
  - TacticalZone one-hot (7 zones + Unknown = 8)
  - NodeType one-hot (player / bombsite / bomb = 3)
  - Team one-hot (T / CT / none = 3)
Total: 2 + 1 + 1 + 1 + 1 + 1 + 8 + 3 + 3 = 21 features per node

Graph-level label: tactic class from RoundTactic column (T-side only).

Multiple snapshots per round (at 10s, 20s, 30s into round) for data augmentation.
"""

import os
import glob
import pandas as pd
import numpy as np
import torch
from torch_geometric.data import Data

TACTICAL_ZONES = [
    "T_SPAWN", "A_APPROACH", "A_SITE", "B_APPROACH",
    "B_SITE", "MID", "CT_SPAWN", "Unknown"
]
ZONE_TO_IDX = {z: i for i, z in enumerate(TACTICAL_ZONES)}

TACTIC_CLASSES = [
    "Rush",
    "Execute",
    "Fake",
    "Split",
    "MidControl",
    "Default",
    "Eco",
]
TACTIC_TO_IDX = {t: i for i, t in enumerate(TACTIC_CLASSES)}


def map_tactic_to_class(label: str) -> str:
    """Map detailed tactic labels to broader classes for training."""
    if not label or label == "":
        return None
    low = label.lower()
    if low.startswith("rush"):
        return "Rush"
    if low.startswith("execute"):
        return "Execute"
    if low.startswith("fake"):
        return "Fake"
    if low.startswith("split"):
        return "Split"
    if low.startswith("midcontrol") or low == "mid_control":
        return "MidControl"
    if low.startswith("eco"):
        return "Eco"
    # Default, Push_*, etc.
    return "Default"


def one_hot_zone(zone: str) -> list:
    vec = [0.0] * len(TACTICAL_ZONES)
    idx = ZONE_TO_IDX.get(zone, ZONE_TO_IDX["Unknown"])
    vec[idx] = 1.0
    return vec


def one_hot_node_type(ntype: str) -> list:
    """player=0, bombsite=1, bomb=2"""
    vec = [0.0, 0.0, 0.0]
    if ntype == "player":
        vec[0] = 1.0
    elif ntype == "bombsite":
        vec[1] = 1.0
    elif ntype == "bomb":
        vec[2] = 1.0
    return vec


def one_hot_team(team: str) -> list:
    """T=0, CT=1, none=2"""
    vec = [0.0, 0.0, 0.0]
    if team == "T":
        vec[0] = 1.0
    elif team == "CT":
        vec[1] = 1.0
    else:
        vec[2] = 1.0
    return vec


def build_player_features(row) -> list:
    return [
        row["X"] / 4096.0,
        row["Y"] / 4096.0,
        row["Health"] / 100.0,
        row["Armor"] / 100.0,
        1.0 if row["IsAlive"] else 0.0,
        1.0 if row["HasC4"] else 0.0,
        row["ViewDirectionX"] / 360.0,
        *one_hot_zone(row["TacticalZone"]),
        *one_hot_node_type("player"),
        *one_hot_team(row["PlayerTeam"]),
    ]


def build_dead_player_features(team: str) -> list:
    return [
        0.0, 0.0,  # X, Y
        0.0, 0.0,  # Health, Armor
        0.0,        # not alive
        0.0,        # no bomb
        0.0,        # no view direction
        *one_hot_zone("Unknown"),
        *one_hot_node_type("player"),
        *one_hot_team(team),
    ]


def build_bombsite_features(zone: str) -> list:
    return [
        0.0, 0.0,  # position not meaningful for abstract bombsite node
        0.0, 0.0,  # no health/armor
        1.0,        # always "alive" (exists)
        0.0,        # no bomb
        0.0,        # no view direction
        *one_hot_zone(zone),
        *one_hot_node_type("bombsite"),
        *one_hot_team("none"),
    ]


def build_bomb_features(x, y, zone) -> list:
    return [
        x / 4096.0,
        y / 4096.0,
        0.0, 0.0,  # no health/armor
        1.0,        # exists
        1.0,        # is the bomb
        0.0,        # no view direction
        *one_hot_zone(zone),
        *one_hot_node_type("bomb"),
        *one_hot_team("none"),
    ]


NUM_NODE_FEATURES = 21  # 2+1+1+1+1+1+8+3+3


def build_graph(tick_df: pd.DataFrame, tactic_label: str, round_num: int,
                map_name: str, snapshot_time: float) -> Data:
    """
    Build a single graph from all player rows at one (Round, Tick).
    """
    tactic_class = map_tactic_to_class(tactic_label)
    if tactic_class is None:
        return None
    class_idx = TACTIC_TO_IDX.get(tactic_class)
    if class_idx is None:
        return None

    nodes = []

    # Separate T and CT players, deduplicate by PlayerName (keep first occurrence)
    t_players = tick_df[tick_df["PlayerTeam"] == "T"].drop_duplicates(subset="PlayerName", keep="first")
    ct_players = tick_df[tick_df["PlayerTeam"] == "CT"].drop_duplicates(subset="PlayerName", keep="first")

    # Cap at 5 per team
    t_players = t_players.head(5)
    ct_players = ct_players.head(5)

    # Add T players (pad to 5)
    for _, row in t_players.iterrows():
        nodes.append(build_player_features(row))
    for _ in range(5 - len(t_players)):
        nodes.append(build_dead_player_features("T"))

    # Add CT players (pad to 5)
    for _, row in ct_players.iterrows():
        nodes.append(build_player_features(row))
    for _ in range(5 - len(ct_players)):
        nodes.append(build_dead_player_features("CT"))

    # Bombsite A node
    nodes.append(build_bombsite_features("A_SITE"))
    # Bombsite B node
    nodes.append(build_bombsite_features("B_SITE"))

    # Bomb node (C4 carrier's position)
    c4_rows = tick_df[tick_df["HasC4"] == True]
    if len(c4_rows) > 0:
        c4 = c4_rows.iloc[0]
        bomb_x, bomb_y, bomb_zone = c4["X"], c4["Y"], c4["TacticalZone"]
    else:
        bomb_x, bomb_y, bomb_zone = 0.0, 0.0, "Unknown"
    nodes.append(build_bomb_features(bomb_x, bomb_y, bomb_zone))

    # Total: 10 players + 2 bombsites + 1 bomb = 13 nodes
    x = torch.tensor(nodes, dtype=torch.float)
    assert x.shape == (13, NUM_NODE_FEATURES), f"Got shape {x.shape}"

    # Fully connected edges (all pairs)
    n = x.shape[0]
    src, dst = [], []
    for i in range(n):
        for j in range(n):
            if i != j:
                src.append(i)
                dst.append(j)
    edge_index = torch.tensor([src, dst], dtype=torch.long)

    y = torch.tensor([class_idx], dtype=torch.long)

    data = Data(x=x, edge_index=edge_index, y=y)
    data.round_num = round_num
    data.map_name = map_name
    data.snapshot_time = snapshot_time
    data.tactic_detail = tactic_label
    return data


def load_and_build_graphs(output_dir: str, snapshot_offsets=(10.0, 20.0, 30.0)):
    """
    Load all *_map_activities.csv from output_dir and build graphs.
    Takes multiple snapshots per round for data augmentation.
    """
    activity_files = sorted(glob.glob(os.path.join(output_dir, "*_map_activities.csv")))
    if not activity_files:
        print(f"No *_map_activities.csv files found in {output_dir}")
        return []

    all_graphs = []

    for act_file in activity_files:
        base = os.path.basename(act_file).replace("_map_activities.csv", "")
        tactic_file = os.path.join(output_dir, base + "_round_tactics.csv")

        if not os.path.exists(tactic_file):
            print(f"Skipping {base}: no round_tactics.csv found")
            continue

        print(f"Loading {base}...")
        df = pd.read_csv(act_file)
        tactics_df = pd.read_csv(tactic_file)

        # Parse boolean columns
        for col in ["IsAlive", "HasC4", "IsInBombZone", "IsInBuyZone"]:
            if col in df.columns:
                df[col] = df[col].map({"true": True, "false": False, True: True, False: False})

        # Build tactic lookup: Round -> T_TacticLabel
        tactic_map = dict(zip(tactics_df["Round"], tactics_df["T_TacticLabel"]))

        # Detect map name from file
        map_name = base.split("-")[-1] if "-" in base else "unknown"

        # Get round start times (first tick time per round)
        round_start = df.groupby("Round")["Time"].min().to_dict()

        # Group by Round
        for round_num, round_df in df.groupby("Round"):
            if round_num not in tactic_map:
                continue

            tactic_label = tactic_map[round_num]
            start_time = round_start.get(round_num, 0)

            for offset in snapshot_offsets:
                target_time = start_time + offset
                available = round_df[round_df["IsAlive"] == True]
                if len(available) == 0:
                    continue

                # Find the tick closest to target_time
                tick_times = available.groupby("Tick")["Time"].first()
                if len(tick_times) == 0:
                    continue
                best_tick = tick_times.index[(tick_times - target_time).abs().argmin()]
                tick_df = available[available["Tick"] == best_tick]

                # Only take alive players
                tick_df = tick_df[tick_df["IsAlive"] == True]
                if len(tick_df) == 0:
                    continue

                graph = build_graph(tick_df, tactic_label, round_num, map_name, offset)
                if graph is not None:
                    all_graphs.append(graph)

        print(f"  Built {len([g for g in all_graphs if g.map_name == map_name])} graphs from {base}")

    print(f"\nTotal graphs: {len(all_graphs)}")
    return all_graphs


def save_graphs(graphs: list, output_path: str):
    """Save graphs list to a .pt file."""
    os.makedirs(os.path.dirname(output_path) or ".", exist_ok=True)
    torch.save(graphs, output_path)
    print(f"Saved {len(graphs)} graphs to {output_path}")


def load_graphs(path: str) -> list:
    """Load graphs list from a .pt file."""
    graphs = torch.load(path, weights_only=False)
    print(f"Loaded {len(graphs)} graphs from {path}")
    return graphs


if __name__ == "__main__":
    import sys
    output_dir = sys.argv[1] if len(sys.argv) > 1 else os.path.join(
        os.path.dirname(__file__), "..", "output"
    )
    save_path = os.path.join(os.path.dirname(__file__), "data", "graphs.pt")

    print(f"Building graphs from: {output_dir}")
    graphs = load_and_build_graphs(output_dir)

    if graphs:
        # Print class distribution
        from collections import Counter
        labels = [TACTIC_CLASSES[g.y.item()] for g in graphs]
        print("\nClass distribution:")
        for cls, count in sorted(Counter(labels).items(), key=lambda x: -x[1]):
            print(f"  {cls}: {count}")

        save_graphs(graphs, save_path)
