"""Create a new sanitized derivative; preserved private attempts are read-only inputs."""
import argparse
import hashlib
import json
import re
from pathlib import Path
from datetime import datetime, timezone

WINDOWS_PATH = re.compile(r"(?i)(?<![a-z0-9_])[a-z]:(?:(?:\\{1,16}|/)[^\\\"'/<>|\r\n,}\]]+)+")
PLACEHOLDER_TAIL = re.compile(r"<local-path>(?:(?:\\{1,16}|/)[^\\\"'/<>|\r\n,}\]]+)+", re.I)
URI_CREDENTIAL = re.compile(r"(?i)(postgres(?:ql)?://)[^/@\s\"']+@")
PASSWORD = re.compile(r"(?i)\b(password|passwd|pgpassword|api[_-]?key)\s*=\s*(?:\"[^\"]*\"|'[^']*'|[^\s\"'&]+)")
SID = re.compile(r"\bS-1-(?:\d+-){2,}\d+\b")
PRIVATE_FIELDS = {"canonical_path", "local_path", "root_path", "root_identity_id", "root_identity_scope", "root_identity_birth_token", "native_id", "native_id_scope", "native_birth_token"}
TEXT_SUFFIXES = {".json", ".jsonl", ".log", ".txt", ".md", ".csv"}
CANARIES = {"M17_EXPORT_CANARY_SECRET": "<credential-canary>", "M17NestedSecret_20260928!": "<credential-canary>", "m17_secret_canary": "<credential-canary>", "M17_CREDENTIAL_CANARY": "<credential-canary>", "M17_CANONICAL_CANARY": "<path-canary>", "M17_RELATIVE_CANARY": "<path-canary>", "M17_NATIVE_IDENTITY_CANARY": "<identity-canary>", "M17_NATIVE_CANARY": "<identity-canary>"}

def sha(data):
    return hashlib.sha256(data).hexdigest()

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--secret-file", required=True)
    parser.add_argument("--exclude-campaign", action="append", default=[], help="Exclude active export recorder campaigns to avoid a self-referential snapshot")
    args = parser.parse_args()
    source = Path(args.source).resolve()
    output = Path(args.output).resolve()
    if output == source or source in output.parents:
        raise RuntimeError("Export must be outside the private attempt tree")
    output.mkdir(parents=True, exist_ok=False)
    secret = Path(args.secret_file).read_text(encoding="utf-8-sig").strip()
    variants = {secret}
    for _ in range(5):
        variants.update(json.dumps(v)[1:-1] for v in tuple(variants))

    def sanitize_text(value):
        for item in sorted(variants, key=len, reverse=True):
            value = value.replace(item, "<redacted-credential>")
        for canary, replacement in CANARIES.items():
            value = re.sub(re.escape(canary), replacement, value, flags=re.I)
        value = WINDOWS_PATH.sub("<host-path>", value)
        value = PLACEHOLDER_TAIL.sub("<host-path>", value)
        value = URI_CREDENTIAL.sub(r"\1<credentials>@", value)
        value = PASSWORD.sub(r"\1=<redacted-credential>", value)
        return SID.sub("<private-sid>", value)

    def sanitize_node(value):
        if isinstance(value, dict):
            return {key: "<private-field>" if key.lower() in PRIVATE_FIELDS and val is not None else sanitize_node(val) for key, val in value.items()}
        if isinstance(value, list):
            return [sanitize_node(v) for v in value]
        if isinstance(value, str):
            return sanitize_text(value)
        return value

    records, excluded = [], []
    for path in sorted(source.rglob("*")):
        if not path.is_file():
            continue
        relative = path.relative_to(source)
        if relative.parts[0] in args.exclude_campaign:
            excluded.append({"source": relative.as_posix(), "reason": "export recorder campaign excluded to avoid a self-referential live log snapshot"})
            continue
        if "corpus" in relative.parts or path.suffix.lower() not in TEXT_SUFFIXES:
            excluded.append({"source": relative.as_posix(), "reason": "binary, database dump, schema SQL, or generated corpus kept private"})
            continue
        original = path.read_bytes()
        if original.startswith((b"\xff\xfe", b"\xfe\xff")):
            text = original.decode("utf-16")
            encoding = "utf-16"
        else:
            try:
                text = original.decode("utf-8-sig")
                encoding = "utf-8"
            except UnicodeDecodeError:
                text = original.decode("cp1252")
                encoding = "cp1252"
        valid_json = None
        if path.suffix.lower() == ".json":
            try:
                value = json.loads(text)
                sanitized = json.dumps(sanitize_node(value), ensure_ascii=False, indent=2) + "\n"
                valid_json = True
            except json.JSONDecodeError:
                sanitized = sanitize_text(text)
                valid_json = False
        elif path.suffix.lower() == ".jsonl":
            lines = []
            for line in text.splitlines():
                try:
                    lines.append(json.dumps(sanitize_node(json.loads(line)), ensure_ascii=False, separators=(",", ":")))
                except json.JSONDecodeError:
                    lines.append(sanitize_text(line))
            sanitized = "\n".join(lines) + ("\n" if lines else "")
        else:
            sanitized = sanitize_text(text)
        if any(v and v in sanitized for v in variants) or WINDOWS_PATH.search(sanitized) or PLACEHOLDER_TAIL.search(sanitized):
            raise RuntimeError("Sanitization validation failed for " + relative.as_posix())
        if valid_json:
            json.loads(sanitized)
        destination = output / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        data = sanitized.encode("utf-8")
        with destination.open("xb") as handle:
            handle.write(data)
        records.append({"source": relative.as_posix(), "original_sha256": sha(original), "export_sha256": sha(data), "original_bytes": len(original), "export_bytes": len(data), "original_encoding": encoding, "original_json_valid": valid_json})
    for record in records:
        if sha((source / record["source"]).read_bytes()) != record["original_sha256"]:
            raise RuntimeError("Private input changed during export: " + record["source"])
    index = {"format_version": 1, "date_utc": datetime.now(timezone.utc).isoformat(), "status": "pass", "originals_unchanged": True, "sanitized_files": len(records), "excluded_files": len(excluded), "transformations": ["absolute host paths", "path suffixes after old placeholders", "credential values", "private identity fields", "Windows security identifiers"], "records": records, "excluded": excluded}
    with (output / "export-index.json").open("x", encoding="utf-8") as handle:
        json.dump(index, handle, ensure_ascii=False, indent=2)
        handle.write("\n")
    print(json.dumps({"status": "pass", "sanitized_files": len(records), "excluded_files": len(excluded), "originals_unchanged": True}))

if __name__ == "__main__":
    main()
