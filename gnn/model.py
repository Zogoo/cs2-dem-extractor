"""
Graph Convolutional Network for CS2 tactic prediction.

Two modes:
  eval mode  (default): 2-layer GCN, hidden=128, dropout=0.1.
             Use for generalisation experiments (80/20 split).
  full mode  (--full):  3-layer GCN, hidden=256, dropout=0.0.
             Train on ALL data — no test split. Higher capacity to
             fully fit the available demos (demo-annotation use case).

Architecture:
  GCNConv layers → ReLU → Dropout
  Global Sum Pooling → Linear classifier
"""

import torch
import torch.nn.functional as F
from torch_geometric.nn import GCNConv, global_add_pool


class TacticGCN(torch.nn.Module):
    def __init__(self, num_node_features: int, num_classes: int,
                 hidden_channels: int = 128, dropout: float = 0.1,
                 num_layers: int = 2):
        super().__init__()
        self.dropout = dropout

        self.convs = torch.nn.ModuleList()
        self.convs.append(GCNConv(num_node_features, hidden_channels))
        for _ in range(num_layers - 1):
            self.convs.append(GCNConv(hidden_channels, hidden_channels))

        self.lin = torch.nn.Linear(hidden_channels, num_classes)

    def forward(self, x, edge_index, batch):
        for conv in self.convs:
            x = conv(x, edge_index)
            x = F.relu(x)
            x = F.dropout(x, p=self.dropout, training=self.training)
        x = global_add_pool(x, batch)
        return self.lin(x)

    def predict_proba(self, x, edge_index, batch):
        return F.softmax(self.forward(x, edge_index, batch), dim=1)
