const audio = document.querySelector('#audio');
const play = document.querySelector('#play');
const pause = document.querySelector('#pause');
const seek = document.querySelector('#seek');
const elapsed = document.querySelector('#elapsed');
const duration = document.querySelector('#duration');
const error = document.querySelector('#error');
const status = document.querySelector('#server-status');

function formatTime(seconds) {
  if (!Number.isFinite(seconds)) return '—';
  return `${Math.floor(seconds / 60)}:${String(Math.floor(seconds % 60)).padStart(2, '0')}`;
}

function setError(message) {
  error.textContent = message;
  error.hidden = !message;
}

async function initialize() {
  try {
    const health = await fetch('/health', { cache: 'no-store' });
    if (!health.ok) throw new Error('Server health check failed');
    const response = await fetch('/api/v1/demo-track');
    if (!response.ok) throw new Error('Track metadata is unavailable');
    const track = await response.json();
    document.querySelector('#track-title').textContent = track.title;
    audio.src = track.stream_url;
    status.textContent = 'Server reachable';
    play.disabled = false;
    pause.disabled = false;
    setError('');
  } catch (cause) {
    status.textContent = 'Server unreachable';
    setError(cause.message);
  }
}

play.addEventListener('click', async () => {
  try { await audio.play(); setError(''); }
  catch { setError('Playback could not start. Check the media file and browser support.'); }
});
pause.addEventListener('click', () => audio.pause());
audio.addEventListener('loadedmetadata', () => {
  duration.textContent = formatTime(audio.duration);
  seek.disabled = !Number.isFinite(audio.duration);
});
audio.addEventListener('timeupdate', () => {
  elapsed.textContent = formatTime(audio.currentTime);
  if (Number.isFinite(audio.duration) && audio.duration > 0) {
    seek.value = String(Math.round(audio.currentTime / audio.duration * 1000));
  }
});
seek.addEventListener('input', () => {
  if (Number.isFinite(audio.duration)) audio.currentTime = Number(seek.value) / 1000 * audio.duration;
});
audio.addEventListener('error', () => setError('Audio is unavailable or cannot be played.'));
initialize();
