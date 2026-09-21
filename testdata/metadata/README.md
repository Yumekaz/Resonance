# Metadata evaluation fixtures

`untagged.mp3` is a tag-stripped copy of [“Try me!” by iamoneabe](https://opengameart.org/content/try-me), explicitly dedicated to CC0. `untagged.flac` is [FLAC decoder testbench subset file 01](https://github.com/ietf-wg-cellar/flac-test-files/tree/main/subset), also CC0. Both were downloaded from their source hosts on 2026-09-20. The MP3's original 95-byte ID3v2 header was removed to make the missing-tag case. These are controlled parser fixtures, not personal media.

SHA-256 after preparation: MP3 `a6bf32d47cb8f4da29fa6dc5b037cb028a2a8d07c49bdf183a71f17c9d2fc802`; FLAC `02c7e60ae7788c2cb6898a0a46985d759f1e62482ddd965a374dfd09a2a8390f`.

Tests generate tagged MP3 and FLAC variants, a PCM WAV, Unicode ID3v2 tags, embedded PNG artwork, and malformed variants in memory. No personal audio is included.
