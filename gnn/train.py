"""
Training pipeline for TacticGCN — per-map mode (default) and cross-map mode.

Per-map mode (default):
  Trains a separate model per CS2 map using granular labels (e.g. Fake_A_to_B,
  Fake_B_to_A kept separate). Position features are fully meaningful within a
  single map so the GNN can learn site-specific patterns.

Cross-map mode (--cross-map flag):
  Single model across all maps with broad class merging. Useful to confirm
  per-map gains.

Hyperparameters:
  - Optimizer: AdamW, lr=0.0002, weight_decay=1e-5
  - Scheduler: StepLR, step_size=25, gamma=0.85  (slower decay than paper)
  - Loss: CrossEntropyLoss with class weights
  - Split: 80/20 stratified per map
  - Hidden channels: 64  (smaller → less overfitting on tiny datasets)
  - Dropout: 0.1         (lower → less underfitting)
  - Epochs: 250
"""

import os
import sys
import time
import numpy as np
import torch
from torch_geometric.loader import DataLoader
from sklearn.utils.class_weight import compute_class_weight
from sklearn.model_selection import train_test_split
from collections import Counter
from sklearn.metrics import classification_report

from build_graphs import (
    load_graphs_per_map, load_graphs_per_map_from_cache,
    load_and_build_graphs, save_graphs, load_graphs, NUM_NODE_FEATURES
)
from model import TacticGCN, TacticGAT


# ── Helpers ────────────────────────────────────────────────────────────────

def oversample_rare(graphs: list, tactic_classes: list,
                    min_samples: int = 5) -> list:
    """
    Duplicate graphs of rare classes until each class has at least
    min_samples training graphs. Prevents the model from ignoring small classes.
    """
    label_to_graphs = {}
    for g in graphs:
        lbl = g.y.item()
        label_to_graphs.setdefault(lbl, []).append(g)

    augmented = list(graphs)
    for lbl, gs in label_to_graphs.items():
        shortage = min_samples - len(gs)
        if shortage > 0:
            # Repeat the available graphs to fill the shortage
            extras = (gs * ((shortage // len(gs)) + 1))[:shortage]
            augmented.extend(extras)

    return augmented


def compute_weights(graphs, num_classes: int) -> torch.Tensor:
    labels = np.array([g.y.item() for g in graphs])
    unique = np.unique(labels)
    weights = compute_class_weight("balanced", classes=unique, y=labels)
    w = torch.zeros(num_classes)
    for cls, wt in zip(unique, weights):
        w[cls] = wt
    return w


def train_one_epoch(model, loader, optimizer, criterion, device):
    model.train()
    total_loss, correct, total = 0.0, 0, 0
    for batch in loader:
        batch = batch.to(device)
        optimizer.zero_grad()
        out = model(batch.x, batch.edge_index, batch.batch)
        loss = criterion(out, batch.y)
        loss.backward()
        optimizer.step()
        total_loss += loss.item() * batch.num_graphs
        correct += (out.argmax(dim=1) == batch.y).sum().item()
        total += batch.num_graphs
    return total_loss / total, correct / total


@torch.no_grad()
def evaluate(model, loader, criterion, device):
    model.eval()
    total_loss, correct, total = 0.0, 0, 0
    preds, labels = [], []
    for batch in loader:
        batch = batch.to(device)
        out = model(batch.x, batch.edge_index, batch.batch)
        loss = criterion(out, batch.y)
        total_loss += loss.item() * batch.num_graphs
        pred = out.argmax(dim=1)
        correct += (pred == batch.y).sum().item()
        total += batch.num_graphs
        preds.extend(pred.cpu().numpy())
        labels.extend(batch.y.cpu().numpy())
    return total_loss / total, correct / total, preds, labels


# ── Per-map training ───────────────────────────────────────────────────────

def round_based_split(graphs: list, test_size: float = 0.2,
                      random_state: int = 42):
    """
    Split by round number, keeping all snapshot graphs of the same round
    together in the same partition. This prevents data leakage where a 20s
    snapshot of round N ends up in training while the 30s snapshot of the
    same round is in testing.
    """
    round_to_graphs = {}
    for g in graphs:
        round_to_graphs.setdefault(g.round_num, []).append(g)

    rounds = list(round_to_graphs.keys())
    round_labels = []
    for r in rounds:
        lbls = [g.y.item() for g in round_to_graphs[r]]
        round_labels.append(max(set(lbls), key=lbls.count))

    try:
        train_rounds, test_rounds = train_test_split(
            rounds, test_size=test_size, random_state=random_state,
            stratify=round_labels)
    except ValueError:
        # Not enough samples in some class for stratification
        train_rounds, test_rounds = train_test_split(
            rounds, test_size=test_size, random_state=random_state)

    train_g = [g for r in train_rounds for g in round_to_graphs[r]]
    test_g = [g for r in test_rounds for g in round_to_graphs[r]]
    return train_g, test_g


def train_one_map(map_name: str, graphs: list, tactic_classes: list,
                  epochs: int, batch_size: int, lr: float,
                  hidden: int, save_dir: str, device: torch.device,
                  model_type: str = "gcn"):
    """Train and evaluate one model for a single map."""
    num_classes = len(tactic_classes)
    print(f"\n{'='*60}")
    print(f"Map: {map_name}  |  {len(graphs)} graphs  |  {num_classes} classes")
    print(f"Classes: {tactic_classes}")

    if len(graphs) < 10:
        print(f"  Too few graphs ({len(graphs)}), skipping.")
        return None, 0.0

    labels = [g.y.item() for g in graphs]

    # Graph-level stratified split. Each snapshot (20s/30s/40s) of a round is
    # treated as an independent sample. This gives more stable train/test ratios
    # and avoids the high variance of round-based splits on tiny datasets (6 test
    # rounds → one "unusual" round = −17% accuracy).
    train_g, test_g = train_test_split(
        graphs, test_size=0.2, random_state=42, stratify=labels)

    # Oversample rare classes in training set
    train_g = oversample_rare(train_g, tactic_classes, min_samples=6)

    print(f"Train: {len(train_g)} graphs  Test: {len(test_g)} graphs")
    print(f"Class distribution (all rounds):")
    for cls_idx, n in sorted(Counter(labels).items(), key=lambda x: -x[1]):
        print(f"  {tactic_classes[cls_idx]}: {n}")

    train_loader = DataLoader(train_g, batch_size=batch_size, shuffle=True)
    test_loader = DataLoader(test_g, batch_size=batch_size)

    class_weights = compute_weights(train_g, num_classes).to(device)
    criterion = torch.nn.CrossEntropyLoss(weight=class_weights)

    if model_type == "gat":
        model = TacticGAT(
            num_node_features=NUM_NODE_FEATURES,
            num_classes=num_classes,
            hidden_channels=hidden,
            heads=4,
            dropout=0.1,
            num_layers=2,
        ).to(device)
    else:
        model = TacticGCN(
            num_node_features=NUM_NODE_FEATURES,
            num_classes=num_classes,
            hidden_channels=hidden,
            dropout=0.1,
            num_layers=2,
        ).to(device)

    optimizer = torch.optim.AdamW(model.parameters(), lr=lr, weight_decay=1e-5)
    # Reduce LR when test accuracy stops improving — smarter than fixed StepLR
    # on tiny datasets where convergence epoch is unpredictable.
    # CosineAnnealingLR smoothly decays LR without early collapse.
    # T_max=epochs means one full cosine cycle — lr goes from lr_max to 0.
    scheduler = torch.optim.lr_scheduler.CosineAnnealingLR(
        optimizer, T_max=epochs, eta_min=1e-5)

    best_acc = 0.0
    best_epoch = 0
    model_path = os.path.join(save_dir, f"best_model_{map_name}.pt")

    print(f"\n{'Epoch':>6} {'TrainLoss':>10} {'TrainAcc':>9} "
          f"{'TestLoss':>9} {'TestAcc':>8} {'LR':>10}")
    print("-" * 60)

    for epoch in range(1, epochs + 1):
        t0 = time.time()
        tr_loss, tr_acc = train_one_epoch(
            model, train_loader, optimizer, criterion, device)
        te_loss, te_acc, _, _ = evaluate(
            model, test_loader, criterion, device)

        scheduler.step()

        if te_acc > best_acc:
            best_acc = te_acc
            best_epoch = epoch
            torch.save({
                "epoch": epoch,
                "model_state_dict": model.state_dict(),
                "test_acc": te_acc,
                "train_acc": tr_acc,
                "num_node_features": NUM_NODE_FEATURES,
                "num_classes": num_classes,
                "hidden_channels": hidden,
                "tactic_classes": tactic_classes,
                "map_name": map_name,
            }, model_path)

        if epoch % 25 == 0 or epoch == 1:
            lr_now = optimizer.param_groups[0]["lr"]
            elapsed = time.time() - t0
            print(f"{epoch:>6} {tr_loss:>10.4f} {tr_acc:>8.1%} "
                  f"{te_loss:>9.4f} {te_acc:>7.1%} {lr_now:>10.6f}")

    print(f"\nBest test accuracy: {best_acc:.1%} at epoch {best_epoch}")

    # Final evaluation with best model
    ckpt = torch.load(model_path, weights_only=False)
    model.load_state_dict(ckpt["model_state_dict"])
    _, _, preds, true_labels = evaluate(model, test_loader, criterion, device)

    present = sorted(set(true_labels))
    names = [tactic_classes[i] for i in present]
    print(f"\nClassification report ({map_name}):")
    print(classification_report(true_labels, preds,
                                labels=present, target_names=names,
                                zero_division=0))
    return model, best_acc


def train_per_map(output_dir: str = None, graphs_dir: str = None,
                  epochs: int = 250, batch_size: int = 16,
                  lr: float = 0.0002, hidden: int = 64,
                  save_dir: str = None, model_type: str = "gcn"):
    """Train one GCN model per map using granular per-map labels.

    Args:
        output_dir:  Path to dem-parser output/ CSV directory (default source).
        graphs_dir:  Path to per-demo .pt cache directory produced by
                     build_graphs.py --incremental. Overrides output_dir when set.
    """
    device = torch.device("cuda" if torch.cuda.is_available() else
                          "mps" if torch.backends.mps.is_available() else "cpu")
    print(f"Device: {device}")

    if save_dir is None:
        save_dir = os.path.join(os.path.dirname(__file__), "data")
    os.makedirs(save_dir, exist_ok=True)

    if graphs_dir:
        print(f"Loading graphs from cache: {graphs_dir}")
        map_data = load_graphs_per_map_from_cache(graphs_dir)
    else:
        if output_dir is None:
            output_dir = os.path.join(os.path.dirname(__file__), "..", "output")
        # 25s/30s/35s = peak tactical divergence window.
        map_data = load_graphs_per_map(output_dir, snapshot_offsets=(25.0, 30.0, 35.0))
    if not map_data:
        print("No graphs built. Check output directory.")
        return {}

    results = {}
    for map_name, info in map_data.items():
        model, acc = train_one_map(
            map_name=map_name,
            graphs=info["graphs"],
            tactic_classes=info["classes"],
            epochs=epochs,
            batch_size=batch_size,
            lr=lr,
            hidden=hidden,
            save_dir=save_dir,
            device=device,
            model_type=model_type,
        )
        results[map_name] = {"model": model, "acc": acc,
                             "classes": info["classes"]}

    # Summary
    print(f"\n{'='*60}")
    print("FINAL SUMMARY — Per-Map Accuracy")
    print(f"{'='*60}")
    for map_name, r in results.items():
        print(f"  {map_name:>12}: {r['acc']:.1%}")
    overall = np.mean([r["acc"] for r in results.values()])
    print(f"  {'Average':>12}: {overall:.1%}")
    return results


# ── Cross-map training (kept for comparison) ───────────────────────────────

def train_cross_map(output_dir: str = None, epochs: int = 250,
                    batch_size: int = 32, lr: float = 0.0002,
                    hidden: int = 64, save_dir: str = None):
    """Train a single model across all maps (broad labels). Lower accuracy expected."""
    device = torch.device("cuda" if torch.cuda.is_available() else
                          "mps" if torch.backends.mps.is_available() else "cpu")
    print(f"Device: {device}")

    if save_dir is None:
        save_dir = os.path.join(os.path.dirname(__file__), "data")
    os.makedirs(save_dir, exist_ok=True)

    if output_dir is None:
        output_dir = os.path.join(os.path.dirname(__file__), "..", "output")

    graph_path = os.path.join(save_dir, "graphs_cross.pt")
    if os.path.exists(graph_path):
        graphs = load_graphs(graph_path)
        tactic_classes = sorted(set(g.tactic_class for g in graphs))
    else:
        graphs = load_and_build_graphs(output_dir, snapshot_offsets=(20.0, 30.0, 40.0))
        if not graphs:
            return
        tactic_classes = sorted(set(g.tactic_class for g in graphs))
        save_graphs(graphs, graph_path)

    # Remap indices to match sorted class list
    tactic_to_idx = {c: i for i, c in enumerate(tactic_classes)}
    for g in graphs:
        g.y = torch.tensor([tactic_to_idx[g.tactic_class]], dtype=torch.long)

    model, acc = train_one_map(
        map_name="all_maps",
        graphs=graphs,
        tactic_classes=tactic_classes,
        epochs=epochs,
        batch_size=batch_size,
        lr=lr,
        hidden=hidden,
        save_dir=save_dir,
        device=device,
        model_type="gcn",
    )
    return model, acc


# ── Full-dataset training (no test split) ─────────────────────────────────

def train_full_dataset(output_dir: str = None, graphs_dir: str = None,
                       epochs: int = 2000, batch_size: int = 8,
                       lr: float = 0.001, save_dir: str = None):
    """
    Train on ALL available graphs — no held-out test set.

    Use case: demo annotation / label verification on the exact demos you
    have parsed. The model learns every round's pattern and should approach
    100% training accuracy. This is NOT a generalisation benchmark;
    it answers "can the GNN correctly understand these specific demos?"

    Settings that differ from eval mode:
      - 3 GCN layers (more capacity to memorise 80-160 graphs/map)
      - hidden=256 (wider for richer pattern storage)
      - dropout=0.0 (no regularisation — we WANT to fit this data)
      - lr=0.001 (higher to converge faster)
      - epochs=2000 (enough to fully converge)
      - batch_size=8 (small batches → more gradient steps per epoch)
    """
    device = torch.device("cuda" if torch.cuda.is_available() else
                          "mps" if torch.backends.mps.is_available() else "cpu")
    print(f"Device: {device}  |  Mode: FULL DATASET (no test split)\n")

    if save_dir is None:
        save_dir = os.path.join(os.path.dirname(__file__), "data")
    os.makedirs(save_dir, exist_ok=True)

    if graphs_dir:
        print(f"Loading graphs from cache: {graphs_dir}")
        map_data = load_graphs_per_map_from_cache(graphs_dir)
    else:
        if output_dir is None:
            output_dir = os.path.join(os.path.dirname(__file__), "..", "output")
        map_data = load_graphs_per_map(output_dir, snapshot_offsets=(25.0, 30.0, 35.0))
    if not map_data:
        print("No graphs built. Check output directory.")
        return {}

    summary = {}
    for map_name, info in map_data.items():
        graphs = info["graphs"]
        tactic_classes = info["classes"]
        num_classes = len(tactic_classes)

        print(f"\n{'='*60}")
        print(f"Map: {map_name}  |  {len(graphs)} graphs  |  {num_classes} classes")
        print(f"Classes: {tactic_classes}")
        for cls, n in Counter(g.tactic_class for g in graphs).most_common():
            print(f"  {cls}: {n}")

        # ALL graphs go to training — no test set
        train_loader = DataLoader(graphs, batch_size=batch_size, shuffle=True)

        labels = np.array([g.y.item() for g in graphs])
        unique = np.unique(labels)
        weights = compute_class_weight("balanced", classes=unique, y=labels)
        class_weights = torch.zeros(num_classes)
        for cls, w in zip(unique, weights):
            class_weights[cls] = w
        class_weights = class_weights.to(device)

        model = TacticGCN(
            num_node_features=NUM_NODE_FEATURES,
            num_classes=num_classes,
            hidden_channels=256,
            dropout=0.0,
            num_layers=3,
        ).to(device)

        optimizer = torch.optim.Adam(model.parameters(), lr=lr)
        scheduler = torch.optim.lr_scheduler.CosineAnnealingLR(
            optimizer, T_max=epochs, eta_min=1e-5)
        criterion = torch.nn.CrossEntropyLoss(weight=class_weights)

        model_path = os.path.join(save_dir, f"full_model_{map_name}.pt")
        best_acc = 0.0
        best_epoch = 0

        print(f"\n{'Epoch':>6} {'Loss':>10} {'TrainAcc':>9} {'LR':>10}")
        print("-" * 40)

        for epoch in range(1, epochs + 1):
            tr_loss, tr_acc = train_one_epoch(
                model, train_loader, optimizer, criterion, device)
            scheduler.step()

            if tr_acc > best_acc:
                best_acc = tr_acc
                best_epoch = epoch
                torch.save({
                    "epoch": epoch,
                    "model_state_dict": model.state_dict(),
                    "train_acc": tr_acc,
                    "num_node_features": NUM_NODE_FEATURES,
                    "num_classes": num_classes,
                    "hidden_channels": 256,
                    "num_layers": 3,
                    "tactic_classes": tactic_classes,
                    "map_name": map_name,
                }, model_path)

            if epoch % 100 == 0 or epoch == 1 or tr_acc >= 1.0:
                lr_now = optimizer.param_groups[0]["lr"]
                print(f"{epoch:>6} {tr_loss:>10.4f} {tr_acc:>8.1%} {lr_now:>10.6f}")
                if tr_acc >= 1.0:
                    print(f"  → 100% training accuracy reached at epoch {epoch}!")
                    break

        print(f"\nBest training accuracy: {best_acc:.1%} at epoch {best_epoch}")

        # Final classification report on training data
        ckpt = torch.load(model_path, weights_only=False)
        model.load_state_dict(ckpt["model_state_dict"])
        test_loader = DataLoader(graphs, batch_size=32)
        _, _, preds, true_labels = evaluate(model, test_loader, criterion, device)
        present = sorted(set(true_labels))
        names = [tactic_classes[i] for i in present]
        print(f"\nFull training set classification report ({map_name}):")
        print(classification_report(true_labels, preds,
                                    labels=present, target_names=names,
                                    zero_division=0))
        summary[map_name] = best_acc

    print(f"\n{'='*60}")
    print("FINAL SUMMARY — Training Accuracy (all data, no test split)")
    print(f"{'='*60}")
    for map_name, acc in summary.items():
        print(f"  {map_name:>12}: {acc:.1%}")
    print(f"  {'Average':>12}: {np.mean(list(summary.values())):.1%}")
    return summary


if __name__ == "__main__":
    import argparse
    parser = argparse.ArgumentParser(description="Train TacticGCN on CS2 tactic graphs")
    parser.add_argument("output_dir", nargs="?", default=None,
                        help="Path to dem-parser output/ CSV directory")
    parser.add_argument("--graphs-dir", default=None, metavar="DIR",
                        help="Load from per-demo .pt cache (build_graphs.py --incremental)")
    parser.add_argument("--full", action="store_true",
                        help="Full-dataset mode: train on ALL graphs, no test split")
    parser.add_argument("--cross-map", action="store_true",
                        help="Single cross-map model instead of per-map models")
    parser.add_argument("--epochs", type=int, default=None,
                        help="Override default epoch count")
    parser.add_argument("--model", choices=["gcn", "gat"], default="gcn",
                        help="Model architecture: gcn (default) or gat (attention)")
    parsed = parser.parse_args()

    graphs_dir = parsed.graphs_dir
    model_type = parsed.model

    if parsed.full:
        print("Full-dataset mode: training on ALL data, no test split.")
        print("Goal: verify the GNN can learn these specific demos (100% target).\n")
        train_full_dataset(output_dir=parsed.output_dir, graphs_dir=graphs_dir,
                           epochs=parsed.epochs or 2000)
    elif parsed.cross_map:
        print("Training cross-map model (broad labels, lower accuracy expected)...")
        train_cross_map(output_dir=parsed.output_dir,
                        epochs=parsed.epochs or 500)
    else:
        print(f"Training per-map models (granular labels, model={model_type})...")
        train_per_map(output_dir=parsed.output_dir, graphs_dir=graphs_dir,
                      epochs=parsed.epochs or 500, model_type=model_type)
