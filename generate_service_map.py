#!/usr/bin/env python3
"""
generate_service_map.py
--------------------------
Generates a list of observed service-to-service dependencies, including
both successful (FORWARDED) and failed (DROPPED) connections.

Output format (one JSON object per edge):

{
  "source": "frontend (default)",
  "destination": "productcatalogservice (default)",
  "protocol": "TCP"
}
"""

import json
import sys
import networkx as nx
import matplotlib.pyplot as plt
from collections import defaultdict

FLOW_FILE = Path("flows.json")  # Change if needed
edges = set()

def workload_and_ns(side: dict) -> str | None:
    """Return 'workload (namespace)' or None if incomplete."""
    ns = side.get("namespace")
    name = None
    if side.get("workloads"):
        name = side["workloads"][0].get("name")
    elif side.get("pod_name"):
        name = side["pod_name"].split("-")[0]  # strip replica suffix
    if name and ns:
        return f"{name} ({ns})"
    return None

def protocol(flow: dict) -> str:
    if "l7" in flow:
        if "http" in flow["l7"]:
            return "HTTP"
        if "grpc" in flow["l7"]:
            return "gRPC"
        return "L7"
    l4 = flow.get("l4", {})
    if "TCP" in l4:
        return "TCP"
    if "UDP" in l4:
        return "UDP"
    return "UNKNOWN"

def load_flows(filename):
    """Load flow data from JSON file."""
    with open(filename) as f:
        return json.load(f)

def build_graph(flows):
    """Build a directed graph from flow data."""
    G = nx.DiGraph()
    edges = defaultdict(int)
    
    for flow in flows:
        src = f"{flow['source']}"
        dst = f"{flow['destination']}"
        edges[(src, dst)] += 1
        
    for (src, dst), count in edges.items():
        G.add_edge(src, dst, weight=count)
    
    return G

def draw_graph(G, output_file):
    """Draw the service dependency graph."""
    plt.figure(figsize=(12, 8))
    pos = nx.spring_layout(G)
    
    nx.draw(G, pos,
            with_labels=True,
            node_color='lightblue',
            node_size=2000,
            font_size=8,
            font_weight='bold',
            arrows=True,
            edge_color='gray')
    
    plt.savefig(output_file)
    plt.close()

def main():
    if len(sys.argv) != 3:
        print("Usage: generate_service_map.py <input_flows.json> <output_graph.png>")
        sys.exit(1)
        
    input_file = sys.argv[1]
    output_file = sys.argv[2]
    
    flows = load_flows(input_file)
    G = build_graph(flows)
    draw_graph(G, output_file)
    print(f"Service map generated: {output_file}")

if __name__ == "__main__":
    main()
