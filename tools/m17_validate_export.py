"""Validate a public derivative against immutable private inputs without editing either."""
import argparse
import hashlib
import importlib.util
import json
from collections import Counter
from pathlib import Path


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--source", required=True)
    p.add_argument("--export", required=True)
    p.add_argument("--secret-file", required=True)
    p.add_argument("--output", required=True)
    a = p.parse_args()
    source, public = Path(a.source), Path(a.export)
    spec = importlib.util.spec_from_file_location("exporter", Path(__file__).with_name("m17_export_evidence.py"))
    exporter = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(exporter)
    secret = Path(a.secret_file).read_text(encoding="utf-8-sig").strip()
    index = json.loads((public / "export-index.json").read_text(encoding="utf-8"))
    failures, states = [], Counter()
    for row in index["records"]:
        rel = row["source"]
        original, derivative = (source / rel).read_bytes(), (public / rel).read_bytes()
        if hashlib.sha256(original).hexdigest() != row["original_sha256"]:
            failures.append({"source": rel, "reason": "original changed after snapshot"})
        if hashlib.sha256(derivative).hexdigest() != row["export_sha256"]:
            failures.append({"source": rel, "reason": "derivative hash mismatch"})
        text = derivative.decode("utf-8")
        if (secret in text or exporter.WINDOWS_PATH.search(text)
                or exporter.PLACEHOLDER_TAIL.search(text) or exporter.SID.search(text)
                or any(canary.lower() in text.lower() for canary in exporter.CANARIES)):
            failures.append({"source": rel, "reason": "private path, credential, identity, or canary remains"})
        if row["original_json_valid"]:
            value = json.loads(text)
            if rel.endswith("/manifest.json"):
                states[value.get("status", "unknown")] += 1
    result = {"status": "pass" if not failures else "fail", "files_checked": len(index["records"]),
              "preserved_attempt_statuses": dict(states), "failures": failures,
              "original_hashes_checked": True, "derivative_hashes_checked": True,
              "privacy_and_json_checked": True}
    with Path(a.output).open("x", encoding="utf-8") as f:
        json.dump(result, f, indent=2)
        f.write("\n")
    print(json.dumps(result))
    if failures:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
