"""
Training pipeline for the TacticGCN model.

Hyperparameters (matching Csapó thesis, Section 4.2):
  - Optimizer: AdamW, lr=0.0002, weight_decay=1e-5
  - Scheduler: StepLR, step_size=10, gamma=0.5
  - Loss: CrossEntropyLoss with class weights (for imbalanced tactics)
  - Split: 80/20 train/test
  - Hidden channels: 128
  - Epochs: 100 (configurable)
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

from build_graphs import (
    load_and_build_graphs, save_graphs, load_graphs,
    TACTIC_CLASSES, NUM_NODE_FEATURES
)
from model import TacticGCN


def compute_weights(graphs):
    """Compute class weights to handle imbalanced tactic distribution."""
    labels = np.array([g.y.item() for g in graphs])
    unique_classes = np.unique(labels)
    weights = compute_class_weight("balanced", classes=unique_classes, y=labels)
    weight_tensor = torch.zeros(len(TACTIC_CLASSES))
    for cls, w in zip(unique_classes, weights):
        weight_tensor[cls] = w
    return weight_tensor


def train_one_epoch(model, loader, optimizer, criterion, device):
    model.train()
    total_loss = 0
    correct = 0
    total = 0

    for batch in loader:
        batch = batch.to(device)
        optimizer.zero_grad()
        out = model(batch.x, batch.edge_index, batch.batch)
        loss = criterion(out, batch.y)
        loss.backward()
        optimizer.step()

        total_loss += loss.item() * batch.num_graphs
        pred = out.argmax(dim=1)
        correct += (pred == batch.y).sum().item()
        total += batch.num_graphs

    return total_loss / total, correct / total


@torch.no_grad()
def evaluate(model, loader, criterion, device):
    model.eval()
    total_loss = 0
    correct = 0
    total = 0
    all_preds = []
    all_labels = []

    for batch in loader:
        batch = batch.to(device)
        out = model(batch.x, batch.edge_index, batch.batch)
        loss = criterion(out, batch.y)

        total_loss += loss.item() * batch.num_graphs
        pred = out.argmax(dim=1)
        correct += (pred == batch.y).sum().item()
        total += batch.num_graphs
        all_preds.extend(pred.cpu().numpy())
        all_labels.extend(batch.y.cpu().numpy())

    return total_loss / total, correct / total, all_preds, all_labels


def train(output_dir: str = None, epochs: int = 100, batch_size: int = 32,
          lr: float = 0.0002, hidden: int = 128, save_dir: str = None):
    """Full training pipeline."""
    device = torch.device("cuda" if torch.cuda.is_available() else
                          "mps" if torch.backends.mps.is_available() else "cpu")
    print(f"Using device: {device}")

    # Load or build graphs
    if save_dir is None:
        save_dir = os.path.join(os.path.dirname(__file__), "data")
    graph_path = os.path.join(save_dir, "graphs.pt")

    if os.path.exists(graph_path):
        graphs = load_graphs(graph_path)
    else:
        if output_dir is None:
            output_dir = os.path.join(os.path.dirname(__file__), "..", "output")
        graphs = load_and_build_graphs(output_dir)
        if not graphs:
            print("No graphs built. Check your output directory.")
            return
        save_graphs(graphs, graph_path)

    print(f"\nDataset: {len(graphs)} graphs, {NUM_NODE_FEATURES} features/node, "
          f"{len(TACTIC_CLASSES)} classes")

    # Class distribution
    labels = [g.y.item() for g in graphs]
    print("\nClass distribution:")
    for cls, count in sorted(Counter(labels).items(), key=lambda x: -x[1]):
        print(f"  {TACTIC_CLASSES[cls]}: {count}")

    # 80/20 stratified split
    train_graphs, test_graphs = train_test_split(
        graphs, test_size=0.2, random_state=42, stratify=labels
    )
    print(f"\nTrain: {len(train_graphs)}, Test: {len(test_graphs)}")

    train_loader = DataLoader(train_graphs, batch_size=batch_size, shuffle=True)
    test_loader = DataLoader(test_graphs, batch_size=batch_size)

    # Class weights
    class_weights = compute_weights(train_graphs).to(device)
    print(f"Class weights: {class_weights.tolist()}")

    # Model
    model = TacticGCN(
        num_node_features=NUM_NODE_FEATURES,
        num_classes=len(TACTIC_CLASSES),
        hidden_channels=hidden,
    ).to(device)

    param_count = sum(p.numel() for p in model.parameters())
    print(f"Model parameters: {param_count:,}")

    # Optimizer and scheduler (matching paper)
    optimizer = torch.optim.AdamW(model.parameters(), lr=lr, weight_decay=1e-5)
    scheduler = torch.optim.lr_scheduler.StepLR(optimizer, step_size=10, gamma=0.5)
    criterion = torch.nn.CrossEntropyLoss(weight=class_weights)

    # Training loop
    best_test_acc = 0
    best_epoch = 0
    model_save_path = os.path.join(save_dir, "best_model.pt")

    print(f"\nTraining for {epochs} epochs...")
    print(f"{'Epoch':>6} {'Train Loss':>11} {'Train Acc':>10} {'Test Loss':>10} "
          f"{'Test Acc':>9} {'LR':>10} {'Time':>6}")
    print("-" * 75)

    for epoch in range(1, epochs + 1):
        t0 = time.time()

        train_loss, train_acc = train_one_epoch(model, train_loader, optimizer,
                                                 criterion, device)
        test_loss, test_acc, _, _ = evaluate(model, test_loader, criterion, device)
        scheduler.step()

        elapsed = time.time() - t0
        current_lr = optimizer.param_groups[0]["lr"]

        if test_acc > best_test_acc:
            best_test_acc = test_acc
            best_epoch = epoch
            torch.save({
                "epoch": epoch,
                "model_state_dict": model.state_dict(),
                "optimizer_state_dict": optimizer.state_dict(),
                "test_acc": test_acc,
                "train_acc": train_acc,
                "num_node_features": NUM_NODE_FEATURES,
                "num_classes": len(TACTIC_CLASSES),
                "hidden_channels": hidden,
                "tactic_classes": TACTIC_CLASSES,
            }, model_save_path)

        if epoch % 5 == 0 or epoch == 1:
            print(f"{epoch:>6} {train_loss:>11.4f} {train_acc:>9.1%} "
                  f"{test_loss:>10.4f} {test_acc:>8.1%} {current_lr:>10.6f} "
                  f"{elapsed:>5.1f}s")

    print(f"\nBest test accuracy: {best_test_acc:.1%} at epoch {best_epoch}")
    print(f"Model saved to: {model_save_path}")

    # Final evaluation
    print("\n--- Final Evaluation on Test Set ---")
    checkpoint = torch.load(model_save_path, weights_only=False)
    model.load_state_dict(checkpoint["model_state_dict"])
    _, test_acc, preds, labels_out = evaluate(model, test_loader, criterion, device)

    from sklearn.metrics import classification_report
    target_names = [TACTIC_CLASSES[i] for i in sorted(set(labels_out))]
    print(classification_report(labels_out, preds, target_names=target_names,
                                zero_division=0))

    return model, test_acc


if __name__ == "__main__":
    output_dir = sys.argv[1] if len(sys.argv) > 1 else None
    train(output_dir=output_dir, epochs=100)
