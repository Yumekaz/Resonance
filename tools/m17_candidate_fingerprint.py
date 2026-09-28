"""Identify the corrected public source without private plans, fixtures, or export recursion."""
import argparse
import hashlib
import json
import platform
import subprocess
from datetime import datetime, timezone
from pathlib import Path


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--output", required=True)
    a = p.parse_args()
    root = Path(__file__).resolve().parent.parent
    def git(*args):
        return subprocess.check_output(["git", "-c", "safe.directory=*", *args], cwd=root, stderr=subprocess.DEVNULL)
    files = sorted(set(git("ls-files", "--cached", "--others", "--exclude-standard", "-z").decode().split("\0")) - {""})
    records = []
    for relative in files:
        if relative.startswith("docs/benchmarks/m1-7-public/"):
            continue
        path = root / relative
        if path.is_file():
            records.append({"file": relative, "sha256": hashlib.sha256(path.read_bytes()).hexdigest()})
    result = {"date_utc": datetime.now(timezone.utc).isoformat(),
              "base_commit": git("rev-parse", "HEAD").decode().strip(),
              "source_tree_dirty": bool(git("status", "--porcelain")),
              "platform": platform.platform(), "source_files": records,
              "compiled_server_sha256": hashlib.sha256((root / "data/m17-closure/resonance.exe").read_bytes()).hexdigest(),
              "exclusions": ["ignored private plans/raw attempts/fixtures/secrets", "public exports to avoid recursive hashing"],
              "runtime": {"go": "1.25.0", "postgresql": "17.11", "chrome": "153.0.8010.53", "playwright": "1.55.1"}}
    with Path(a.output).open("x", encoding="utf-8") as f:
        json.dump(result, f, indent=2)
        f.write("\n")
    print(json.dumps({"status": "pass", "source_file_count": len(records), "base_commit": result["base_commit"], "compiled_server_sha256": result["compiled_server_sha256"]}))


if __name__ == "__main__":
    main()
