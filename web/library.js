const nav = document.querySelector('nav');
const items = document.querySelector('#items');
const status = document.querySelector('#status');
const title = document.querySelector('#view-title');
const more = document.querySelector('#more');
const audio = document.querySelector('#audio');
const nowTitle = document.querySelector('#now-title');
const nowCredit = document.querySelector('#now-credit');
const cover = document.querySelector('#cover');
const playerError = document.querySelector('#player-error');
let view = 'tracks';
let group = null;
let cursor = null;
let loading = false;

function node(tag, text, className) {
  const element = document.createElement(tag);
  element.textContent = text;
  if (className) element.className = className;
  return element;
}

function endpoint() {
  if (group) return `/api/v1/${group.kind}/${encodeURIComponent(group.id)}/${view}`;
  return `/api/v1/${view}`;
}

function label(item) {
  if (view === 'artists') return item.display_credit;
  if (view === 'albums') return item.display_title;
  return item.title || 'Untitled track';
}

function sublabel(item) {
  if (view === 'tracks') return [item.artist_credit || 'Unknown artist', item.album_title || 'Ungrouped'].join(' · ');
  return item.available ? 'Available' : 'Unavailable';
}

function addItem(item) {
  const li = document.createElement('li');
  const button = node('button', label(item), 'item-title');
  button.type = 'button';
  button.addEventListener('click', () => {
    if (view === 'tracks') playTrack(item);
    else openGroup(view, item);
  });
  if (view === 'tracks' && !item.available) button.disabled = true;
  li.append(button, node('span', sublabel(item), 'item-subtitle'));
  if (view === 'tracks' && !item.available) li.append(node('span', 'Unavailable', 'badge'));
  items.append(li);
}

async function load(reset = false) {
  if (loading) return;
  loading = true;
  if (reset) { cursor = null; items.replaceChildren(); }
  more.hidden = true;
  status.textContent = 'Loading…';
  try {
    const url = new URL(endpoint(), location.origin);
    url.searchParams.set('limit', '50');
    if (cursor) url.searchParams.set('cursor', cursor);
    const response = await fetch(url, { cache: 'no-store' });
    if (!response.ok) throw new Error('The catalog is unavailable. Try again shortly.');
    const page = await response.json();
    for (const item of page.items) addItem(item);
    cursor = page.next_cursor;
    more.hidden = !cursor;
    status.textContent = items.children.length ? `${items.children.length} shown` : 'No entries in this view';
  } catch (cause) {
    status.textContent = cause.message;
  } finally { loading = false; }
}

function selectView(nextView) {
  view = nextView;
  group = null;
  title.textContent = nextView[0].toUpperCase() + nextView.slice(1);
  for (const button of nav.querySelectorAll('button')) button.setAttribute('aria-current', button.dataset.view === view ? 'page' : 'false');
  load(true);
}

function openGroup(kind, item) {
  const heading = label(item);
  group = { kind, id: item.id };
  view = kind === 'artists' ? 'albums' : 'tracks';
  title.replaceChildren(node('button', '← Back', 'back'), document.createTextNode(` ${heading}`));
  title.querySelector('button').addEventListener('click', () => selectView(kind));
  load(true);
}

function playTrack(track) {
  if (!track.available) return;
  playerError.hidden = true;
  nowTitle.textContent = track.title || 'Untitled track';
  nowCredit.textContent = track.artist_credit || 'Unknown artist';
  if (track.artwork_url) { cover.src = track.artwork_url; cover.hidden = false; }
  else { cover.removeAttribute('src'); cover.hidden = true; }
  audio.src = track.stream_url;
  audio.play().catch(() => {
    playerError.textContent = audio.error ? 'This audio is unavailable or cannot be decoded by this browser.' : 'Playback could not start. Check browser format support or try another track.';
    playerError.hidden = false;
  });
}

nav.addEventListener('click', event => { const button = event.target.closest('button[data-view]'); if (button) selectView(button.dataset.view); });
more.addEventListener('click', () => load());
audio.addEventListener('error', () => { playerError.textContent = 'This audio is unavailable or cannot be decoded by this browser.'; playerError.hidden = false; });
selectView('tracks');
