"""
GCN and GAT models for CS2 tactic prediction.

TacticGCN (default):
  2-layer GCN, hidden=128, dropout=0.1.

TacticGAT (--model gat):
  2-layer GAT with multi-head attention (heads=4, hidden=128).
  Attention heads learn to focus on tactically relevant nodes
  (T-side players in approach zones), useful with fine-grained zones.

Both use Global Sum Pooling → Linear classifier.
"""

import torch
import torch.nn.functional as F
from torch_geometric.nn import GCNConv, GATConv, global_add_pool


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


class TacticGAT(torch.nn.Module):
    def __init__(self, num_node_features: int, num_classes: int,
                 hidden_channels: int = 128, heads: int = 4,
                 dropout: float = 0.1, num_layers: int = 2):
        super().__init__()
        self.dropout = dropout
        per_head = hidden_channels // heads

        self.convs = torch.nn.ModuleList()
        self.convs.append(GATConv(num_node_features, per_head, heads=heads, dropout=dropout))
        for _ in range(num_layers - 1):
            self.convs.append(GATConv(hidden_channels, per_head, heads=heads, dropout=dropout))

        self.lin = torch.nn.Linear(hidden_channels, num_classes)

    def forward(self, x, edge_index, batch):
        for conv in self.convs:
            x = conv(x, edge_index)
            x = F.elu(x)
            x = F.dropout(x, p=self.dropout, training=self.training)
        x = global_add_pool(x, batch)
        return self.lin(x)

    def predict_proba(self, x, edge_index, batch):
        return F.softmax(self.forward(x, edge_index, batch), dim=1)
