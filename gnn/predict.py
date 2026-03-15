"""
Predict tactics on new demo data using a trained TacticGCN model.

Usage:
  python predict.py                           # predict on all output/ CSVs
  python predict.py /path/to/output_dir       # predict on specific directory
  python predict.py /path/to/output_dir model.pt  # use specific model
"""

import os
import sys
import torch
from torch_geometric.loader import DataLoader
from collections import Counter

from build_graphs import (
    load_and_build_graphs, TACTIC_CLASSES, NUM_NODE_FEATURES
)
from model import TacticGCN


def load_model(model_path: str, device: torch.device):
    checkpoint = torch.load(model_path, weights_only=False, map_location=device)
    model = TacticGCN(
        num_node_features=checkpoint["num_node_features"],
        num_classes=checkpoint["num_classes"],
        hidden_channels=checkpoint["hidden_channels"],
    ).to(device)
    model.load_state_dict(checkpoint["model_state_dict"])
    model.eval()
    return model, checkpoint


@torch.no_grad()
def predict_graphs(model, graphs, device):
    """Predict one graph at a time to safely access custom attributes."""
    results = []

    for g in graphs:
        data = g.to(device)
        batch_vec = torch.zeros(data.x.shape[0], dtype=torch.long, device=device)
        out = model(data.x, data.edge_index, batch_vec)
        probs = torch.softmax(out, dim=1)
        pred_idx = out.argmax(dim=1).item()

        pred_class = TACTIC_CLASSES[pred_idx]
        confidence = probs[0][pred_idx].item()
        actual_class = TACTIC_CLASSES[data.y.item()]

        results.append({
            "round": int(g.round_num) if hasattr(g, "round_num") else 0,
            "map": str(g.map_name) if hasattr(g, "map_name") else "?",
            "snapshot_time": float(g.snapshot_time) if hasattr(g, "snapshot_time") else 0,
            "predicted": pred_class,
            "confidence": confidence,
            "actual": actual_class,
            "actual_detail": str(g.tactic_detail) if hasattr(g, "tactic_detail") else "",
            "correct": pred_class == actual_class,
        })

    return results


def predict(output_dir: str = None, model_path: str = None):
    device = torch.device("cuda" if torch.cuda.is_available() else
                          "mps" if torch.backends.mps.is_available() else "cpu")
    print(f"Device: {device}")

    if model_path is None:
        model_path = os.path.join(os.path.dirname(__file__), "data", "best_model.pt")

    if not os.path.exists(model_path):
        print(f"Model not found at {model_path}. Run train.py first.")
        return

    model, checkpoint = load_model(model_path, device)
    print(f"Loaded model (epoch {checkpoint['epoch']}, "
          f"test acc: {checkpoint['test_acc']:.1%})")

    if output_dir is None:
        output_dir = os.path.join(os.path.dirname(__file__), "..", "output")

    # Build graphs from CSVs (single snapshot at 20s for prediction)
    graphs = load_and_build_graphs(output_dir, snapshot_offsets=(20.0,))
    if not graphs:
        print("No graphs built from output directory.")
        return

    results = predict_graphs(model, graphs, device)

    # Print per-round results
    print(f"\n{'Round':>6} {'Map':>12} {'Predicted':>12} {'Confidence':>11} "
          f"{'Actual':>12} {'Detail':>20} {'':>2}")
    print("-" * 85)

    correct = 0
    total = 0
    for r in sorted(results, key=lambda x: (str(x["map"]), x["round"])):
        mark = "OK" if r["correct"] else "XX"
        print(f"{r['round']:>6} {r['map']:>12} {r['predicted']:>12} "
              f"{r['confidence']:>10.1%} {r['actual']:>12} "
              f"{r['actual_detail']:>20} {mark:>2}")
        if r["correct"]:
            correct += 1
        total += 1

    print(f"\nAccuracy: {correct}/{total} = {correct/total:.1%}")

    # Prediction distribution
    pred_dist = Counter(r["predicted"] for r in results)
    print("\nPrediction distribution:")
    for cls, count in sorted(pred_dist.items(), key=lambda x: -x[1]):
        print(f"  {cls}: {count}")


if __name__ == "__main__":
    output_dir = sys.argv[1] if len(sys.argv) > 1 else None
    model_path = sys.argv[2] if len(sys.argv) > 2 else None
    predict(output_dir=output_dir, model_path=model_path)
