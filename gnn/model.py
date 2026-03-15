"""
2-Layer Graph Convolutional Network for CS2 tactic prediction.

Architecture (matching Csapó thesis, Section 4.2):
  1. GCNConv(num_features, 128) + ReLU + Dropout
  2. GCNConv(128, 128) + ReLU + Dropout
  3. Global Sum Pooling (node embeddings -> graph embedding)
  4. Linear(128, num_classes)

GCN was chosen over GAT because with small graphs (<13 nodes),
the attention mechanism adds no benefit (paper Table 2).
"""

import torch
import torch.nn.functional as F
from torch_geometric.nn import GCNConv, global_add_pool


class TacticGCN(torch.nn.Module):
    def __init__(self, num_node_features: int, num_classes: int,
                 hidden_channels: int = 128, dropout: float = 0.3):
        super().__init__()
        self.conv1 = GCNConv(num_node_features, hidden_channels)
        self.conv2 = GCNConv(hidden_channels, hidden_channels)
        self.lin = torch.nn.Linear(hidden_channels, num_classes)
        self.dropout = dropout

    def forward(self, x, edge_index, batch):
        # Layer 1
        x = self.conv1(x, edge_index)
        x = F.relu(x)
        x = F.dropout(x, p=self.dropout, training=self.training)

        # Layer 2
        x = self.conv2(x, edge_index)
        x = F.relu(x)
        x = F.dropout(x, p=self.dropout, training=self.training)

        # Global Sum Pooling: aggregate all node embeddings into one graph vector
        x = global_add_pool(x, batch)

        # Classification head
        x = self.lin(x)
        return x

    def predict_proba(self, x, edge_index, batch):
        """Return softmax probabilities instead of raw logits."""
        logits = self.forward(x, edge_index, batch)
        return F.softmax(logits, dim=1)
