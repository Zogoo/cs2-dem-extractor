"""
2-Layer Graph Convolutional Network for CS2 tactic prediction.

Architecture (matching Csapó thesis, Section 4.2):
  1. GCNConv(num_features, hidden) + ReLU + Dropout
  2. GCNConv(hidden, hidden) + ReLU + Dropout
  3. Global Sum Pooling
  4. Linear(hidden, num_classes)

Dropout default is 0.1 (not 0.3) because datasets are small —
lower dropout prevents underfitting when N < 200 training graphs.
"""

import torch
import torch.nn.functional as F
from torch_geometric.nn import GCNConv, global_add_pool


class TacticGCN(torch.nn.Module):
    def __init__(self, num_node_features: int, num_classes: int,
                 hidden_channels: int = 128, dropout: float = 0.1):
        super().__init__()
        self.conv1 = GCNConv(num_node_features, hidden_channels)
        self.conv2 = GCNConv(hidden_channels, hidden_channels)
        self.lin = torch.nn.Linear(hidden_channels, num_classes)
        self.dropout = dropout

    def forward(self, x, edge_index, batch):
        x = self.conv1(x, edge_index)
        x = F.relu(x)
        x = F.dropout(x, p=self.dropout, training=self.training)

        x = self.conv2(x, edge_index)
        x = F.relu(x)
        x = F.dropout(x, p=self.dropout, training=self.training)

        x = global_add_pool(x, batch)
        return self.lin(x)

    def predict_proba(self, x, edge_index, batch):
        return F.softmax(self.forward(x, edge_index, batch), dim=1)
