#!/usr/bin/env python3
"""
generate_service_map.py
--------------------------
Reads flow records from a JSON Lines file and generates a service dependency map,
including both forwarded and dropped flows.

Input format (from boutique-flows.json):
{
    "source_pod": "frontend-77b645c847-wp9tx",
    "source_namespace": "boutique",
    "destination_pod": "productcatalogservice-54fbfd95bb-df9df",
    "destination_namespace": "boutique",
    "l4_protocol": "TCP",
    "verdict": "FORWARDED|DROPPED"
}

Output format:
[
  {
    "source": "frontend (boutique)",
    "destination": "productcatalogservice (boutique)",
    "protocol": "TCP",
    "status": "FORWARDED|DROPPED"
  }
]
"""

import json
import sys
import argparse
from collections import defaultdict

def extract_service_name(pod_name):
    """Extract the service name from a pod name by removing the hash/replica suffix."""
    if not pod_name:
        return None
    # Split on first hyphen followed by alphanumeric characters
    parts = pod_name.split('-')
    if len(parts) > 0:
        return parts[0]
    return None

def process_flows(input_file):
    # Use a dictionary to store unique edges with their status
    edges = defaultdict(lambda: {"forwarded": False, "dropped": False})
    
    try:
        with open(input_file, 'r') as f:
            for line in f:
                try:
                    # Skip empty lines
                    if not line.strip():
                        continue
                        
                    flow = json.loads(line)
                    
                    # Extract source information if available
                    src_pod = extract_service_name(flow.get("source_pod"))
                    src_ns = flow.get("source_namespace")
                    
                    # Extract destination information
                    dst_pod = extract_service_name(flow.get("destination_pod"))
                    dst_ns = flow.get("destination_namespace")
                    
                    # Skip if we don't have enough information to create an edge
                    if not dst_pod or not dst_ns:
                        continue

                    # For source, if we don't have pod/namespace info, mark as "external"
                    source = f"{src_pod} ({src_ns})" if src_pod and src_ns else "external"
                    destination = f"{dst_pod} ({dst_ns})"

                    if source == destination:
                        continue  # Skip self-referential flows

                    protocol = flow.get("l4_protocol", "UNKNOWN")
                    verdict = flow.get("verdict", "UNKNOWN")
                    
                    # Create a unique key for the edge
                    edge_key = (source, destination, protocol)
                    
                    # Update edge information
                    if verdict == "FORWARDED":
                        edges[edge_key]["forwarded"] = True
                    elif verdict == "DROPPED":
                        edges[edge_key]["dropped"] = True

                except json.JSONDecodeError as e:
                    print(f"Warning: Invalid JSON on line: {e}", file=sys.stderr)
                    continue
                except Exception as e:
                    print(f"Error processing flow: {e}", file=sys.stderr)
                    continue

        # Convert the edges to the output format
        output_edges = []
        for (src, dst, proto), status in edges.items():
            edge = {
                "source": src,
                "destination": dst,
                "protocol": proto,
                "status": "MIXED" if status["forwarded"] and status["dropped"] else 
                         "FORWARDED" if status["forwarded"] else 
                         "DROPPED"
            }
            output_edges.append(edge)

        return output_edges

    except Exception as e:
        print(f"Error reading or processing file: {e}", file=sys.stderr)
        sys.exit(1)

def main():
    parser = argparse.ArgumentParser(description='Generate service dependency map from flow records')
    parser.add_argument('input_file', help='JSON Lines file containing flow records (one per line)')
    parser.add_argument('--output', '-o', help='Output file (default: stdout)')
    
    args = parser.parse_args()
    
    edges = process_flows(args.input_file)
    
    # Sort edges for consistent output
    edges.sort(key=lambda x: (x["source"], x["destination"], x["protocol"]))
    
    # Output the results
    output_json = json.dumps(edges, indent=2)
    if args.output:
        with open(args.output, 'w') as f:
            f.write(output_json)
    else:
        print(output_json)

if __name__ == "__main__":
    main()
