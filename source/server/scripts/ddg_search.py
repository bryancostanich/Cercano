#!/usr/bin/env python3
"""DuckDuckGo search script for Cercano.

Accepts a search query and max results count, outputs JSON array of results
to stdout. Designed to be called as a subprocess by the Go server.

Usage:
    python3 ddg_search.py --query "search terms" --max-results 5
"""

import argparse
import json
import sys

from ddgs import DDGS


def main():
    parser = argparse.ArgumentParser(description="DuckDuckGo search for Cercano")
    parser.add_argument("--query", required=True, help="Search query string")
    parser.add_argument(
        "--max-results", type=int, default=5, help="Maximum number of results"
    )
    args = parser.parse_args()

    try:
        results = []
        with DDGS() as ddgs:
            for r in ddgs.text(args.query, max_results=args.max_results):
                results.append(
                    {
                        "url": r.get("href", ""),
                        "title": r.get("title", ""),
                        "snippet": r.get("body", ""),
                    }
                )
        json.dump(results, sys.stdout)
    except Exception as e:
        print(f"search error: {e}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
