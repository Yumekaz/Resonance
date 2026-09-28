"""Independent percentile and interval audit of M1.7 raw measurements."""
import argparse
import calendar
import json
import math
import re
import statistics
from collections import defaultdict
from datetime import datetime, timezone, timedelta
from pathlib import Path

def timestamp_ns(value):
    match = re.fullmatch(r"(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d+))?(Z|[+-]\d\d:\d\d)", value)
    if not match:
        raise ValueError("Malformed request/operation timestamp")
    seconds = calendar.timegm(datetime.strptime(match[1], "%Y-%m-%dT%H:%M:%S").timetuple())
    if match[3] != "Z":
        sign = 1 if match[3][0] == "+" else -1
        offset = int(match[3][1:3]) * 3600 + int(match[3][4:]) * 60
        seconds -= sign * offset
    return seconds * 1_000_000_000 + int((match[2] or "").ljust(9, "0")[:9])

def percentiles(values):
    values = sorted(values)
    return {"count": len(values), "p50_ms": statistics.median(values), "p95_ms": values[math.ceil(len(values)*.95)-1], "p99_ms": values[math.ceil(len(values)*.99)-1]}

def jsonl(path):
    return [json.loads(line) for line in path.read_text(encoding="utf-8-sig").splitlines() if line.strip()]

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--concurrency", required=True)
    parser.add_argument("--watcher", required=True)
    parser.add_argument("--playlist")
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    root = Path(args.concurrency)
    scans = jsonl(root / "scans.jsonl")
    requests = jsonl(root / "requests.jsonl")
    claimed = json.loads((root / "summary.json").read_text())
    assert len(scans) == 30
    per_sample, request_distributions = [], defaultdict(list)
    totals = {phase: defaultdict(int) for phase in ("scan", "discovery", "publication")}
    windows = {}
    for scan in scans:
        result = scan["result"]
        assert result["status"] == "succeeded" and result["files_hashed"] == 1 and result["metadata_extractions"] == 1
        assert result["traversal_complete"] and result["observations_applied"] and result["absence_reconciled"] and scan["expected_grouping_observed"]
        windows[scan["index"]] = {
            "scan": (timestamp_ns(scan["started_utc"]), timestamp_ns(scan["finished_utc"])),
            "discovery": (timestamp_ns(scan["started_utc"]), timestamp_ns(scan["discovery_last_observed_utc"])),
            "publication": (timestamp_ns(scan["transaction_first_observed_utc"]), timestamp_ns(scan["transaction_last_observed_utc"])),
        }
        assert windows[scan["index"]]["publication"][0] >= timestamp_ns(scan["transaction_started_utc"])
        assert all(start < end for start, end in windows[scan["index"]].values())
    counts = {index: {phase: defaultdict(int) for phase in totals} for index in windows}
    unresolved_intervals = []
    for request in requests:
        start, end = timestamp_ns(request["started_utc"]), timestamp_ns(request["finished_utc"])
        assert start <= end and request["request_id"] and not request.get("error")
        assert request["status"] == (206 if request["kind"] == "range" else 200)
        if request["kind"] == "range":
            assert request["bytes"] == 65536
        if start == end:
            unresolved_intervals.append({"sample": request["sample"], "kind": request["kind"], "request_id": request["request_id"], "reason": "equal wall-clock timestamps; no positive interval can be proven; retained in raw artifact and excluded from overlap/latency claims"})
            continue
        latency = (end-start)/1_000_000
        request_distributions[request["kind"]].append(latency)
        for phase, (op_start, op_end) in windows[request["sample"]].items():
            if max(start, op_start) < min(end, op_end):
                counts[request["sample"]][phase][request["kind"]] += 1
                totals[phase][request["kind"]] += 1
                request_distributions[phase+"_"+request["kind"]].append(latency)
    for index, phases in counts.items():
        for phase, kinds in phases.items():
            assert all(kinds[kind] >= 1 for kind in ("catalog", "range", "queue_reorder")), (index, phase, dict(kinds))
        per_sample.append({"index": index, "overlaps": phases})
    scan_percentiles = percentiles([s["result"]["duration_ms"] for s in scans])
    for key in ("p50_ms", "p95_ms", "p99_ms"):
        assert abs(scan_percentiles[key]-claimed[key]) < .001
    watcher = json.loads(Path(args.watcher).read_text())
    assert len(watcher["samples"]) == 30
    assert all(s["files_hashed"] == s["metadata_extractions"] == 1 and s["absence_reconciled"] and s["expected_catalog_title_observed"] for s in watcher["samples"])
    watcher_percentiles = percentiles([s["latency_ms"] for s in watcher["samples"]])
    assert all(abs(watcher_percentiles[k]-watcher[k]) < .001 for k in ("p50_ms", "p95_ms", "p99_ms"))
    prior_claims = {"scan": claimed["scan_overlapping_requests"], "discovery": claimed["discovery_core_overlapping_requests"], "publication": claimed["publication_core_overlapping_requests"]}
    corrections = {phase: {kind: prior_claims[phase].get(kind,0)-totals[phase].get(kind,0) for kind in ("catalog","range","queue_reorder")} for phase in totals}
    assert all(0 <= count <= len(unresolved_intervals) for phase in corrections.values() for count in phase.values())
    report = {"status": "pass", "scan_samples": 30, "request_samples": len(requests), "positive_interval_request_samples": len(requests)-len(unresolved_intervals), "unresolved_intervals_retained_but_not_claimed": unresolved_intervals, "prior_summary_overlap_count_corrections": corrections, "scan_percentiles": scan_percentiles, "watcher_percentiles": watcher_percentiles, "every_sample_each_kind_overlaps_discovery_and_publication": True, "actual_interval_overlap_counts": totals, "per_sample": per_sample, "request_percentiles": {key: percentiles(val) for key, val in request_distributions.items()}, "publication_wrapper_percentiles": percentiles([s["result"]["publish_transaction_ms"] for s in scans]), "postgresql_observed_publication_core_percentiles": percentiles([(windows[s["index"]]["publication"][1]-windows[s["index"]]["publication"][0])/1_000_000 for s in scans])}
    if args.playlist:
        playlist = jsonl(Path(args.playlist))
        assert len(playlist) == 30 and all(s["status"] == 200 and s["playlist_entries"] == 5000 and s["request_id"] for s in playlist)
        report["playlist_5000_percentiles"] = percentiles([s["duration_ms"] for s in playlist])
    output = Path(args.output)
    with output.open("x", encoding="utf-8") as handle:
        json.dump(report, handle, indent=2)
    print(json.dumps({key: report[key] for key in ("status", "scan_samples", "request_samples", "scan_percentiles", "watcher_percentiles", "every_sample_each_kind_overlaps_discovery_and_publication")}))

if __name__ == "__main__":
    main()
