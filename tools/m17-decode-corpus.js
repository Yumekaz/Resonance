// Explicit fixture validation only; never a product route or runtime worker.
const fs = require('node:fs/promises');
const path = require('node:path');
const crypto = require('node:crypto');
const { chromium } = require('@playwright/test');

async function files(dir) {
  const found = [];
  for (const entry of await fs.readdir(dir, { withFileTypes: true })) {
    const name = path.join(dir, entry.name);
    if (entry.isDirectory()) found.push(...await files(name));
    else if (entry.isFile() && entry.name.endsWith('.mp3')) found.push(name);
  }
  return found;
}

async function main() {
  const corpus = process.argv[2];
  const output = process.env.RESONANCE_M17_ATTEMPT_DIR;
  if (!corpus || !output) throw new Error('Corpus and attempt output are required');
  const input = (await files(corpus)).sort();
  if (input.length !== 10000) throw new Error('Expected exactly 10000 MP3 fixtures');
  const raw = await fs.open(path.join(output, 'decoder-samples.jsonl'), 'wx');
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  const page = await browser.newPage();
  await page.goto('about:blank');
  await page.evaluate(() => { window.m17AudioContext = new AudioContext(); });
  let failures = 0, bytes = 0;
  const unique = new Set();
  try {
    for (let first = 0; first < input.length; first += 32) {
      const batch = await Promise.all(input.slice(first, first + 32).map(async (name, offset) => {
        const data = await fs.readFile(name);
        bytes += data.length;
        const sha = crypto.createHash('sha256').update(data).digest('hex');
        unique.add(sha);
        return { index: first + offset + 1, sha256: sha, encoded_bytes: data.length, base64: data.toString('base64') };
      }));
      const results = await page.evaluate(async entries => Promise.all(entries.map(async entry => {
        const binary = atob(entry.base64), data = new Uint8Array(binary.length);
        for (let i = 0; i < binary.length; i++) data[i] = binary.charCodeAt(i);
        const started = performance.now();
        try {
          const decoded = await window.m17AudioContext.decodeAudioData(data.buffer);
          return { index: entry.index, sha256: entry.sha256, encoded_bytes: entry.encoded_bytes, success: decoded.length > 0 && decoded.duration > 0, decoded_samples: decoded.length, sample_rate: decoded.sampleRate, duration_seconds: decoded.duration, decode_ms: performance.now() - started };
        } catch (error) {
          return { index: entry.index, sha256: entry.sha256, encoded_bytes: entry.encoded_bytes, success: false, error: error.name, decode_ms: performance.now() - started };
        }
      })), batch);
      for (const result of results) {
        if (!result.success) failures++;
        await raw.write(JSON.stringify(result) + '\n');
      }
    }
  } finally {
    await raw.close();
    await browser.close();
  }
  const summary = { date_utc: new Date().toISOString(), fixtures: input.length, unique_sha256: unique.size, encoded_bytes: bytes, decoded: input.length - failures, failures, decoder: 'Chrome Web Audio decodeAudioData; fresh browser profile; all 10000 MP3 files', limitation: 'One browser codec implementation; no claim of audible human playback' };
  await fs.writeFile(path.join(output, 'decoder-summary.json'), JSON.stringify(summary, null, 2), { flag: 'wx' });
  console.log(JSON.stringify(summary));
  if (failures || unique.size !== 10000) throw new Error('Distinct encoded fixture validation failed');
}
main().catch(error => { console.error(error.message); process.exitCode = 1; });
