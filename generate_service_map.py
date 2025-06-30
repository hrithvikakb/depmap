#!/usr/bin/env python3
"""
generate_service_map.py
--------------------------
Reads streaming JSON flow records from stdin and generates a list of unique
service-to-service dependency edges.

Input format (streaming JSON, one object per line):
{
    "source_pod": "frontend-abcdef-12345",
    "source_namespace": "default",
    "destination_pod": "cartservice-ghijkl-67890",
    "destination_namespace": "default",
    "l4_protocol": "TCP",
    ...
}


Output format (JSON array of unique edges):
[
  {
    "source": "frontend (default)",
    "destination": "cartservice (default)",
    "protocol": "TCP"
  }
]
"""

import json
import sys
import signal

def main():
    # Use a set to store unique dependency edges
    edges = set()

    # Gracefully handle SIGINT (Ctrl+C)
    def signal_handler(sig, frame):
        # Convert set of tuples to list of dicts and print as JSON
        output_edges = [
            {"source": src, "destination": dst, "protocol": proto}
            for src, dst, proto in sorted(list(edges))
        ]
        print(json.dumps(output_edges, indent=2))
        sys.exit(0)

    signal.signal(signal.SIGINT, signal_handler)

    print("Reading flows from stdin... Press Ctrl+C to generate the service map.", file=sys.stderr)

    for line in sys.stdin:
        try:
            flow = json.loads(line)

            # Extract source information
            src_pod = flow.get("SourcePod", "").split('-')[0]
            src_ns = flow.get("SourceNamespace")
            if not src_pod or not src_ns:
                continue # Skip if source is incomplete
            source = f"{src_pod} ({src_ns})"

            # Extract destination information
            dst_pod = flow.get("DestinationPod", "").split('-')[0]
            dst_ns = flow.get("DestinationNamespace")
            if not dst_pod or not dst_ns:
                continue # Skip if destination is incomplete
            destination = f"{dst_pod} ({dst_ns})"

            protocol = flow.get("L4Protocol", "UNKNOWN")

            # Add the unique edge to the set
            if source != destination:
                edges.add((source, destination, protocol))

        except json.JSONDecodeError:
            # Ignore lines that are not valid JSON
            continue
        except Exception as e:
            print(f"Error processing flow: {e}", file=sys.stderr)


if __name__ == "__main__":
    main()
