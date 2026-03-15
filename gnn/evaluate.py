"""
Evaluation tools: confusion matrix, ROC curves, per-class metrics.

Generates publication-quality plots matching the paper's Figure 2 and 3.
"""

import os
import sys
import numpy as np
import torch
from torch_geometric.loader import DataLoader
from sklearn.metrics import (
    confusion_matrix, classification_report, roc_curve, auc,
    f1_score, accuracy_score
)
from sklearn.preprocessing import label_binarize
from sklearn.model_selection import train_test_split
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

from build_graphs import load_graphs, TACTIC_CLASSES, NUM_NODE_FEATURES
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
def get_predictions(model, loader, device):
    all_preds = []
    all_labels = []
    all_probs = []

    for batch in loader:
        batch = batch.to(device)
        out = model(batch.x, batch.edge_index, batch.batch)
        probs = torch.softmax(out, dim=1)
        pred = out.argmax(dim=1)

        all_preds.extend(pred.cpu().numpy())
        all_labels.extend(batch.y.cpu().numpy())
        all_probs.append(probs.cpu().numpy())

    return (np.array(all_labels), np.array(all_preds),
            np.concatenate(all_probs, axis=0))


def plot_confusion_matrix(labels, preds, class_names, save_path):
    cm = confusion_matrix(labels, preds)
    fig, ax = plt.subplots(figsize=(10, 8))
    im = ax.imshow(cm, interpolation="nearest", cmap=plt.cm.Blues)
    ax.figure.colorbar(im, ax=ax)

    ax.set(
        xticks=np.arange(len(class_names)),
        yticks=np.arange(len(class_names)),
        xticklabels=class_names,
        yticklabels=class_names,
        ylabel="Actual Tactic",
        xlabel="Predicted Tactic",
        title="Confusion Matrix - CS2 Tactic Prediction",
    )
    plt.setp(ax.get_xticklabels(), rotation=45, ha="right", rotation_mode="anchor")

    thresh = cm.max() / 2.0
    for i in range(len(class_names)):
        for j in range(len(class_names)):
            ax.text(j, i, format(cm[i, j], "d"),
                    ha="center", va="center",
                    color="white" if cm[i, j] > thresh else "black")

    fig.tight_layout()
    fig.savefig(save_path, dpi=150, bbox_inches="tight")
    plt.close(fig)
    print(f"Confusion matrix saved to {save_path}")


def plot_roc_curves(labels, probs, class_names, save_path):
    n_classes = len(class_names)
    labels_bin = label_binarize(labels, classes=list(range(n_classes)))

    fig, ax = plt.subplots(figsize=(10, 8))

    colors = plt.cm.Set1(np.linspace(0, 1, n_classes))

    all_fpr = []
    all_tpr = []
    roc_aucs = {}

    for i in range(n_classes):
        if labels_bin[:, i].sum() == 0:
            continue
        fpr, tpr, _ = roc_curve(labels_bin[:, i], probs[:, i])
        roc_auc = auc(fpr, tpr)
        roc_aucs[class_names[i]] = roc_auc
        ax.plot(fpr, tpr, color=colors[i], lw=2,
                label=f"{class_names[i]} (AUC = {roc_auc:.2f})")
        all_fpr.append(fpr)
        all_tpr.append(tpr)

    # Micro-average ROC
    fpr_micro, tpr_micro, _ = roc_curve(labels_bin.ravel(), probs.ravel())
    auc_micro = auc(fpr_micro, tpr_micro)
    ax.plot(fpr_micro, tpr_micro, color="navy", lw=3, linestyle="--",
            label=f"Micro-average (AUC = {auc_micro:.2f})")

    # Random classifier baseline
    ax.plot([0, 1], [0, 1], "k--", lw=1, alpha=0.5, label="Random (AUC = 0.50)")

    ax.set(
        xlim=[0.0, 1.0], ylim=[0.0, 1.05],
        xlabel="False Positive Rate",
        ylabel="True Positive Rate",
        title="ROC Curves - CS2 Tactic Prediction",
    )
    ax.legend(loc="lower right", fontsize=8)
    fig.tight_layout()
    fig.savefig(save_path, dpi=150, bbox_inches="tight")
    plt.close(fig)
    print(f"ROC curves saved to {save_path}")

    return roc_aucs, auc_micro


def plot_training_history(history: dict, save_path: str):
    fig, (ax1, ax2) = plt.subplots(1, 2, figsize=(14, 5))

    epochs = range(1, len(history["train_loss"]) + 1)

    ax1.plot(epochs, history["train_loss"], "b-", label="Train Loss")
    ax1.plot(epochs, history["test_loss"], "r-", label="Test Loss")
    ax1.set(xlabel="Epoch", ylabel="Loss", title="Training & Test Loss")
    ax1.legend()
    ax1.grid(True, alpha=0.3)

    ax2.plot(epochs, history["train_acc"], "b-", label="Train Accuracy")
    ax2.plot(epochs, history["test_acc"], "r-", label="Test Accuracy")
    ax2.set(xlabel="Epoch", ylabel="Accuracy", title="Training & Test Accuracy")
    ax2.legend()
    ax2.grid(True, alpha=0.3)

    fig.tight_layout()
    fig.savefig(save_path, dpi=150, bbox_inches="tight")
    plt.close(fig)
    print(f"Training history saved to {save_path}")


def full_evaluation(data_dir: str = None, model_path: str = None):
    """Run full evaluation: metrics, confusion matrix, ROC curves."""
    if data_dir is None:
        data_dir = os.path.join(os.path.dirname(__file__), "data")
    if model_path is None:
        model_path = os.path.join(data_dir, "best_model.pt")

    device = torch.device("cuda" if torch.cuda.is_available() else
                          "mps" if torch.backends.mps.is_available() else "cpu")

    # Load model
    model, checkpoint = load_model(model_path, device)
    print(f"Loaded model from epoch {checkpoint['epoch']} "
          f"(test acc: {checkpoint['test_acc']:.1%})")

    # Load graphs and split same way as training
    graph_path = os.path.join(data_dir, "graphs.pt")
    graphs = load_graphs(graph_path)

    labels_all = [g.y.item() for g in graphs]
    _, test_graphs = train_test_split(
        graphs, test_size=0.2, random_state=42, stratify=labels_all
    )
    test_loader = DataLoader(test_graphs, batch_size=32)

    # Get predictions
    labels, preds, probs = get_predictions(model, test_loader, device)

    # Metrics
    acc = accuracy_score(labels, preds)
    f1_macro = f1_score(labels, preds, average="macro", zero_division=0)
    f1_weighted = f1_score(labels, preds, average="weighted", zero_division=0)

    print(f"\n{'='*50}")
    print(f"Test Accuracy:     {acc:.1%}")
    print(f"F1 (macro):        {f1_macro:.4f}")
    print(f"F1 (weighted):     {f1_weighted:.4f}")
    print(f"{'='*50}")

    # Per-class report
    present_classes = sorted(set(labels) | set(preds))
    target_names = [TACTIC_CLASSES[i] for i in present_classes]
    print("\nPer-class metrics:")
    print(classification_report(labels, preds, labels=present_classes,
                                target_names=target_names, zero_division=0))

    # Plots
    plots_dir = os.path.join(data_dir, "plots")
    os.makedirs(plots_dir, exist_ok=True)

    plot_confusion_matrix(labels, preds, target_names,
                          os.path.join(plots_dir, "confusion_matrix.png"))

    roc_aucs, auc_micro = plot_roc_curves(
        labels, probs,
        [TACTIC_CLASSES[i] for i in range(len(TACTIC_CLASSES))],
        os.path.join(plots_dir, "roc_curves.png")
    )

    print(f"\nMicro-average AUC: {auc_micro:.4f}")
    print("Per-class AUC:")
    for cls, auc_val in sorted(roc_aucs.items(), key=lambda x: -x[1]):
        print(f"  {cls}: {auc_val:.4f}")


if __name__ == "__main__":
    data_dir = sys.argv[1] if len(sys.argv) > 1 else None
    full_evaluation(data_dir=data_dir)
