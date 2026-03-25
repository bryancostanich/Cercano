#!/usr/bin/env python3
"""
PostToolUse hook for Claude Code that captures cloud token usage from the
transcript and writes it to Cercano's telemetry database.

Reads hook input (JSON) from stdin, parses the transcript JSONL to extract
cumulative cloud token usage, computes the delta since last report, and
inserts into the cloud_usage table.

Usage in Claude Code settings.json:
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "mcp__cercano__.*",
        "command": "python3 /path/to/report_cloud_tokens.py"
      }
    ]
  }
}
"""

import json
import os
import sqlite3
import sys

STATE_FILE = os.path.expanduser("~/.config/cercano/hook_state.json")
DB_PATH = os.path.expanduser("~/.config/cercano/telemetry.db")


def read_hook_input():
    """Read the hook input JSON from stdin."""
    try:
        return json.loads(sys.stdin.read())
    except (json.JSONDecodeError, EOFError):
        return None


def parse_transcript(transcript_path):
    """Parse transcript JSONL and return cumulative cloud token counts."""
    total_input = 0
    total_output = 0
    total_cache_creation = 0
    total_cache_read = 0

    try:
        with open(transcript_path, "r") as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                try:
                    entry = json.loads(line)
                except json.JSONDecodeError:
                    continue

                if entry.get("type") != "assistant":
                    continue

                usage = entry.get("message", {}).get("usage", {})
                if not usage:
                    continue

                total_input += usage.get("input_tokens", 0)
                total_output += usage.get("output_tokens", 0)
                total_cache_creation += usage.get("cache_creation_input_tokens", 0)
                total_cache_read += usage.get("cache_read_input_tokens", 0)
    except (OSError, IOError):
        return None

    return {
        "input_tokens": total_input,
        "output_tokens": total_output,
        "cache_creation_input_tokens": total_cache_creation,
        "cache_read_input_tokens": total_cache_read,
        "total_tokens": total_input + total_output + total_cache_creation + total_cache_read,
    }


def load_state(session_id):
    """Load the last-reported cumulative totals for this session."""
    try:
        with open(STATE_FILE, "r") as f:
            state = json.load(f)
        return state.get(session_id, {})
    except (OSError, IOError, json.JSONDecodeError):
        return {}


def save_state(session_id, totals):
    """Save cumulative totals so next invocation can compute the delta."""
    state = {}
    try:
        with open(STATE_FILE, "r") as f:
            state = json.load(f)
    except (OSError, IOError, json.JSONDecodeError):
        pass

    state[session_id] = totals

    os.makedirs(os.path.dirname(STATE_FILE), exist_ok=True)
    with open(STATE_FILE, "w") as f:
        json.dump(state, f)


def write_to_db(delta_input, delta_output):
    """Insert cloud usage delta into Cercano's telemetry database."""
    if delta_input <= 0 and delta_output <= 0:
        return

    try:
        conn = sqlite3.connect(DB_PATH)
        conn.execute(
            """INSERT INTO cloud_usage
               (timestamp, cloud_input_tokens, cloud_output_tokens, cloud_provider, cloud_model)
               VALUES (datetime('now'), ?, ?, 'anthropic', 'claude-code')""",
            (delta_input, delta_output),
        )
        conn.commit()
        conn.close()
    except sqlite3.Error as e:
        print(f"cercano hook: db error: {e}", file=sys.stderr)


def main():
    hook_input = read_hook_input()
    if not hook_input:
        return

    transcript_path = hook_input.get("transcript_path", "")
    session_id = hook_input.get("session_id", "unknown")

    if not transcript_path or not os.path.exists(transcript_path):
        return

    # Parse current cumulative totals from transcript
    totals = parse_transcript(transcript_path)
    if totals is None:
        return

    # Load previous state and compute delta
    prev = load_state(session_id)
    delta_input = totals["input_tokens"] - prev.get("input_tokens", 0)
    delta_output = totals["output_tokens"] - prev.get("output_tokens", 0)

    # Write delta to telemetry DB
    write_to_db(delta_input, delta_output)

    # Save current state for next invocation
    save_state(session_id, totals)


if __name__ == "__main__":
    main()
