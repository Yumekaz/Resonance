# M1.1 metadata dependency evaluation — independently reviewed

## Decision and supported evaluation profile

Pin `github.com/dhowden/tag` at `3d75831295e8`, BSD-2-Clause. The adapter is an internal evaluation component; no scanner or public metadata endpoint invokes it in M1.1. Its dependency source and license were inspected, and `go mod verify` passed.

The adapter admits FLAC and MP3 with ID3v1 or ordinary ID3v2.3/v2.4 tags. Extended/unsynchronized/compressed/encrypted/grouped ID3 features and ID3v2.2 are explicitly unsupported in this evaluated profile. OGG, MP4, DSF, and WAV are rejected before the dependency can dispatch to unreviewed parsers. This is narrower than the dependency's advertised support and is not a promise of general MP3/FLAC audio validity.

| Independently tested case | Result |
|---|---|
| ID3v2.3 MP3 | Title/artist/album extracted; a separate generated TYER fixture verifies year 2024 |
| ID3v2.4 MP3 | Unicode title extracted |
| ID3v1 MP3 | Title and year extracted |
| Tagged FLAC | Title/artist/album/year extracted |
| Unicode FLAC and MP3 | `夜の歌 ♫` and `Björk` preserved |
| Untagged MP3 | Explicit no-tags error; no fallback title invented |
| Untagged FLAC | Nullable missing fields |
| MP3 APIC / FLAC picture | MIME and opaque image bytes extracted; no image decoding occurs |
| PCM WAV | Unsupported metadata; M0 WAV byte streaming is unchanged |
| Duration/sample rate/channels/bitrate | Remain nil; the dependency does not supply these reliably |
| Invalid/missing FLAC date | Nil, including malformed dates that the dependency turns into year 1 |
| Truncated ID3/enclosing blocks | Rejected |
| Excessive duplicate ID3 frames | Rejected before the dependency's quadratic duplicate-name loop |
| FLAC nested base64 picture with malicious length | Rejected before image allocation |
| Oversized artwork | Rejected by declared-size admission limits |

Corpus provenance and hashes are in `testdata/metadata/README.md`; the two checked-in audio hashes were independently reproduced. Tags, malformed variants, WAV, and large-file fixtures are generated locally. No personal audio or FFmpeg is used.

## Defects found in the submitted wrapper

An 87-byte FLAC containing `METADATA_BLOCK_PICTURE` in a Vorbis comment bypassed the direct-picture precheck. The dependency allocated 33,560,376 bytes and returned success despite the malformed picture. The cumulative 8 MiB source-read ceiling did not bound that allocation. The corrected wrapper rejects the same input after 91 source-read bytes with 416 allocated bytes in the focused regression measurement.

The original wrapper also accepted a tag whose frame extended beyond its declared ID3 envelope, accepted excessive duplicate frames without a work bound, and reported year 1 for an invalid FLAC date. The independent regressions failed before the fixes and pass after them. The original tagged-MP3 corpus did not contain a year frame, so its year claim was unsupported until the new explicit TYER test.

## Admission and resource boundaries

- 8 MiB cumulative source reads, including preflight and dependency passes.
- 4 MiB metadata-envelope limit.
- At most 1,024 ID3 frames, FLAC blocks, or comments per comment block.
- 64 KiB ordinary text/frame limit; 2 MiB picture-data/APIC-frame limit.
- FLAC internal lengths must fit their enclosing block; direct and base64-encoded pictures are checked. Recognized blocks must consume their declared extent.
- The dependency receives only admitted formats/flags. Unsupported encodings return an explicit error rather than being guessed.

These are input/work limits, not an 8 MiB heap limit. Text decoding, maps, base64 decoding, and returned artwork also allocate. Images are not decoded, so this evaluation makes no pixel-allocation or image-validity claim. A blocking underlying reader is not forcibly canceled by this adapter. Future integration must retain these restrictions and handle parse errors explicitly.

## Independent measurements (2026-09-20)

Windows_NT 10.0.26200 x64; AMD Ryzen 7 5700U, 16 logical CPUs, 16,473,051,136 bytes RAM; Go 1.25.0; one benchmark process, warm local file cache. Raw output: `M1-1-review-bench.txt`.

```powershell
go test -count=1 -v ./internal/metadata
go test -run '^$' -bench 'BenchmarkMetadataSparseMP3|BenchmarkReview' -benchmem -count=3 ./internal/metadata
go test -run '^$' -fuzz FuzzMetadataEnvelope -fuzztime=15s -parallel=1 ./internal/metadata
```

| Workload | Three measured ns/op values | Bytes/op | Allocations/op |
|---|---|---:|---:|
| 64 MiB sparse MP3, short generated tags | 81,025 / 80,216 / 62,291 | 984 | 45 |
| 1 MiB opaque artwork payload | 359,016 / 318,638 / 239,770 | about 1,057,760 | 28 |
| Reject 2,048 duplicate ID3 frames | 50,808 / 50,342 / 49,871 | 16,472 | 1,028 |

The sparse MP3 corpus test reads 251 bytes after hardening. The artwork benchmark measures metadata byte extraction, not PNG decoding. The old 201-byte / 920-B allocation figures describe the pre-review wrapper and are superseded. Measurements establish no scan-throughput or latency SLO.

The 15-second single-worker fuzz run executed 357,758 inputs without a failure. This is bounded evidence, not proof of parser safety for all input. Subprocess isolation remains deferred for this restricted, checked adapter: direct validation fixes the observed cases without another runtime/process boundary. The raw dependency is NOT approved for unrestricted input. A new unbounded behavior would require revisiting the dependency or isolation decision before expanding support.
