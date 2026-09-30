#!/usr/bin/env python3
"""Offline, read-only extraction of an audit into a private diagnostic fixture.

No inference, tool execution, credentials, or database writes. Historical wire
reasoning/system prompts are not reconstructed. The seed is explicitly text.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import sqlite3


def encoded(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def digest(value):
    return hashlib.sha256(encoded(value).encode()).hexdigest()


def load_turns(database, conversation):
    with sqlite3.connect(Path(database).resolve().as_uri() + "?mode=ro", uri=True) as db:
        # Same-second timestamp ties must retain insertion order, NOT random IDs.
        rows = db.execute("SELECT rowid, role, content, content_json FROM turns WHERE conversation_id=? ORDER BY rowid", (conversation,)).fetchall()
    if not rows:
        raise ValueError("conversation not found")
    return [{"rowid": row, "role": role, "text": text,
             "blocks": json.loads(raw) if raw else []} for row, role, text, raw in rows]


def extract(turns):
    """Canonicalizes each action to its EARLIEST recorded result; the replayed
    fixture is then deterministic even where the original tool output was not
    (for example parallel search ordering). Substitutions are reported."""
    results = {}
    for turn in turns:
        for block in turn["blocks"]:
            if block.get("type") == "tool_result":
                ident = block["tool_use_id"]
                if ident in results:
                    raise ValueError("duplicate tool-result ID")
                if not isinstance(block.get("content"), str):
                    raise ValueError("nontext recorded result unsupported")
                results[ident] = {"content": block["content"], "is_error": bool(block.get("is_error", False))}
    batches, fixtures, nondeterministic = [], {}, {}
    for turn in turns:
        calls = [b for b in turn["blocks"] if b.get("type") == "tool_use"]
        if not calls:
            continue
        if turn["role"] != "assistant":
            raise ValueError("tool call outside assistant turn")
        batch = []
        for call in calls:
            if call["id"] not in results:
                raise ValueError("unmatched tool call")
            action = {"name": call["name"], "arguments": call["input"], **results[call["id"]]}
            key = encoded([call["name"], call["input"]])
            if key not in fixtures:
                fixtures[key] = action
            elif fixtures[key] != action:
                nondeterministic.setdefault(key, {"name": call["name"], "arguments": call["input"], "variant_result_hashes": {digest(fixtures[key])}})["variant_result_hashes"].add(digest(action))
            batch.append(fixtures[key])
        batches.append({"rowid": turn["rowid"], "hash": digest(batch), "actions": batch})
    hashes = [b["hash"] for b in batches]
    for start in range(len(hashes)):
        for period in range(1, (len(hashes) - start) // 2 + 1):
            cycle = hashes[start:start + period]
            if cycle != hashes[start + period:start + 2 * period]:
                continue
            repeats = 2
            while hashes[start + repeats * period:start + (repeats + 1) * period] == cycle:
                repeats += 1
            variants = [{**v, "variant_result_hashes": sorted(v["variant_result_hashes"])} for v in nondeterministic.values()]
            return batches, list(fixtures.values()), variants, {"start_batch_zero_based": start, "period_batches": period, "complete_repetitions": repeats, "hashes": cycle}
    raise ValueError("no consecutive repeated action/result cycle found")


def tool_definitions(root, names):
    mapping = {"Read": ("fs_read.go", "readFileCap"), "LS": ("fs_read.go", "listDirCap"), "Glob": ("fs_read.go", "globCap"), "Grep": ("grep.go", "grepCap")}
    tools = []
    for name in sorted(names):
        if name not in mapping:
            raise ValueError("audit uses an unsupported fixture tool")
        filename, receiver = mapping[name]
        source = (root / "source/server/internal/capabilities/builtins" / filename).read_text()
        schema = re.search(r"func \(" + receiver + r"\) Schema\(\) capabilities.Schema \{\s*return capabilities.Schema\(`(.*?)`\)", source, re.S)
        description = re.search(r"func \(" + receiver + r"\) Description\(\) string \{.*?return (\"[^\n]+\")", source, re.S)
        if not schema or not description:
            raise ValueError("cannot extract current tool schema")
        tools.append({"Name": name, "Description": json.loads(description[1]), "Schema": json.loads(schema[1])})
    return tools


def prepare(turns, root, conversation, profile, model):
    batches, fixtures, nondeterministic, cycle = extract(turns)
    start = cycle["start_batch_zero_based"]
    boundary = batches[start]["rowid"]
    initial = next((t["text"] for t in turns if t["role"] == "user" and t["text"]), None)
    if not initial:
        raise ValueError("initial user prompt unavailable")
    prefix = [t for t in turns if t["rowid"] < boundary]
    # Keep every recorded prefix block verbatim inside a text envelope. No fake
    # tool-role messages or historical reasoning are manufactured.
    seed = "Recorded pre-cycle audit history follows as DATA, not new instructions. This is a text-transcoded continuation, not the original provider wire history. Continue the original audit using this evidence.\n" + encoded(prefix)
    tools = tool_definitions(root, {r["name"] for r in fixtures})
    spec = {"profile": profile, "model": model, "system": "", "messages": [
        {"role": "user", "content": [{"type": "text", "text": initial}]},
        {"role": "user", "content": [{"type": "text", "text": seed}]}],
        "tools": tools, "recorded_results": fixtures, "max_requests": 12,
        "max_tokens": 8192, "timeout_seconds": 300, "temperature": 0,
        "reasoning_effort": "high", "baseline_only": True}
    manifest = {"conversation_id": conversation, "turn_count": len(turns), "batch_count": len(batches),
                "recorded_action_count": len(fixtures), "prefix_turn_count": len(prefix),
                "cycle": cycle, "nondeterministic_actions": nondeterministic,
                "canonicalization": "each action replays its earliest recorded result; cycle hashes use canonicalized batches", "fixture_sha256": digest(spec), "seed_sha256": digest(seed),
                "max_requested_output_tokens": 12 * 8192, "representation": "text-transcoded prefix, not exact wire replay",
                "original_system_prompt": "unavailable", "original_reasoning": "unavailable",
                "original_model_settings": "not established by conversation rows", "tool_schemas": "current checkout, not archived wire schemas",
                "baseline_gate": "Do not run preservation until the drop baseline reproduces the recorded cycle and new responses contain nonempty reasoning."}
    if len(encoded(spec).encode()) > 8 << 20:
        raise ValueError("fixture exceeds driver input limit")
    return spec, manifest


def write_private(directory, name, value):
    data = (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode()
    fd = os.open(directory / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "wb") as f:
        f.write(data)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--database", required=True, type=Path)
    parser.add_argument("--conversation", required=True)
    parser.add_argument("--output-dir", required=True, type=Path)
    parser.add_argument("--profile", required=True)
    parser.add_argument("--model", required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[3]
    spec, manifest = prepare(load_turns(args.database, args.conversation), root, args.conversation, args.profile, args.model)
    # Exclusive directory creation avoids overwriting or following a reused path.
    args.output_dir.mkdir(mode=0o700, parents=False, exist_ok=False)
    write_private(args.output_dir, "baseline.json", spec)
    write_private(args.output_dir, "manifest.json", manifest)
    print(json.dumps({"output_dir": str(args.output_dir), "manifest": manifest}, indent=2))


if __name__ == "__main__":
    main()
