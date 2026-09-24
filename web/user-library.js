const userItems = document.querySelector('#items');
const userStatus = document.querySelector('#status');
const userTitle = document.querySelector('#view-title');
const userMore = document.querySelector('#more');
const userAudio = document.querySelector('#audio');
const userError = document.querySelector('#player-error');
const queueControls = document.querySelector('#queue-controls');
const playlistCreate = document.querySelector('#playlist-create');
const trackActions = document.querySelector('#track-actions');
const playlistTarget = document.querySelector('#playlist-target');
let activeView = null;
let queue = null;
let currentPlaylist = null;
let favoriteIDs = new Set();
let favoriteLoadGeneration = 0;
let currentPlayback = null;
let viewCursor = null;
let lastClock = performance.now();
let stalled = false;
let starting = false;
const instanceID = sessionStorage.getItem('resonance.instance') || crypto.randomUUID();
sessionStorage.setItem('resonance.instance', instanceID);
const pendingMutationStorageKey = 'resonance.pending_mutations';

function pendingMutations() {
  try {
    const value = JSON.parse(sessionStorage.getItem(pendingMutationStorageKey) || '[]');
    return Array.isArray(value) ? value.filter(item => item && typeof item.identity === 'string' && typeof item.key === 'string' && typeof item.method === 'string' && typeof item.path === 'string' && typeof item.body === 'string') : [];
  } catch { return []; }
}
function savePendingMutations(items) {
  sessionStorage.setItem(pendingMutationStorageKey, JSON.stringify(items));
}
function removePendingMutation(identity, key) {
  savePendingMutations(pendingMutations().filter(item => item.identity !== identity || item.key !== key));
}
function mutationIdentity(method, path, body) {
  const intent = body && typeof body === 'object' ? { ...body } : body;
  if (intent && typeof intent === 'object') delete intent.expected_version;
  return `${method}\n${path}\n${JSON.stringify(intent)}`;
}

function uiNode(tag, value, className) {
  const element = document.createElement(tag);
  element.textContent = value;
  if (className) element.className = className;
  return element;
}
function uiAction(label, fn) {
  const button = uiNode('button', label);
  button.type = 'button';
  button.addEventListener('click', event => { event.stopPropagation(); Promise.resolve(fn()).catch(userFailure); });
  return button;
}
function userFailure(error) {
  userStatus.textContent = error?.code === 'stale_selection' ? 'Queue selection changed in another tab. Refresh the queue.' :
    error?.code === 'stale_version' ? 'This list changed in another tab. Refresh and try again.' :
    'The change was not saved. Check the server and try again.';
}
async function userAPI(path, options = {}) {
  let response;
  try { response = await fetch(path, { cache: 'no-store', ...options }); }
  catch { throw { code: 'catalog_unavailable' }; }
  if (response.status === 204) return null;
  const body = await response.json().catch(() => null);
  if (!response.ok) throw { code: body?.error?.code || 'catalog_unavailable', status: response.status };
  return body;
}
async function userWrite(method, path, body, receipt = true) {
  const serialized = JSON.stringify(body);
  if (!receipt) return userAPI(path, { method, headers: { 'Content-Type': 'application/json' }, body: serialized });
  const identity = mutationIdentity(method, path, body);
  const pending = pendingMutations();
  let saved = pending.find(item => item.identity === identity);
  if (!saved) {
    if (pending.length >= 16) throw { code: 'catalog_unavailable' };
    saved = { identity, key: crypto.randomUUID(), method, path, body: serialized };
    pending.push(saved);
    savePendingMutations(pending);
  }
  try {
    const result = await userAPI(path, { method, headers: { 'Content-Type': 'application/json', 'Idempotency-Key': saved.key }, body: saved.body });
    removePendingMutation(identity, saved.key);
    return result;
  } catch (error) {
    if (error.status >= 400 && error.status < 500) removePendingMutation(identity, saved.key);
    throw error;
  }
}
async function refreshQueue() { queue = await userAPI('/api/v1/queue'); return queue; }
async function refreshFavorites() {
  const found = new Set(); let cursor = null;
  do {
    const page = await userAPI(`/api/v1/favorites?limit=200${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`);
    for (const item of page.items) found.add(item.track_id);
    cursor = page.next_cursor;
  } while (cursor);
  favoriteIDs = found;
}
async function refreshPlaylists() {
  const old = playlistTarget.value;
  playlistTarget.replaceChildren(new Option('Choose playlist', ''));
  let cursor = null;
  do {
    const page = await userAPI(`/api/v1/playlists?limit=200${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`);
    for (const item of page.items) playlistTarget.add(new Option(item.name, item.id));
    cursor = page.next_cursor;
  } while (cursor);
  playlistTarget.value = old;
}
function setControls(mode) {
  queueControls.hidden = mode !== 'queue';
  playlistCreate.hidden = mode !== 'playlists' || !!currentPlaylist;
  trackActions.hidden = mode !== 'tracks';
}
function onBrowseView(mode) { activeView = null; currentPlaylist = null; setControls(mode); }
function selectUserView(mode) {
  activeView = mode; currentPlaylist = null; viewCursor = null;
  userTitle.textContent = mode[0].toUpperCase() + mode.slice(1);
  userItems.replaceChildren(); userMore.hidden = true; setControls(mode);
  for (const button of document.querySelectorAll('nav button[data-view]')) button.setAttribute('aria-current', button.dataset.view === mode ? 'page' : 'false');
  loadUserView().catch(userFailure);
}
async function addQueue(track, placement) {
  if (!track?.id) return;
  const q = await refreshQueue();
  const change = await userWrite('POST', '/api/v1/queue/items', { track_id: track.id, placement, expected_version: q.revision });
  await refreshQueue();
  if (placement === 'now' && change?.item_id && change.current_item_id === change.item_id && change.selection_token)
    window.resonanceBrowse.playTrack(track, { itemID: change.item_id, token: change.selection_token });
  if (activeView === 'queue') await loadUserView(true);
}
function decorateTrackRow(row, track) {
  const controls = uiNode('span', '', 'item-actions');
  controls.append(uiAction('Add to queue', () => addQueue(track, 'end')),
    uiAction('Play next', () => addQueue(track, 'next')),
    uiAction('Play now', () => addQueue(track, 'now')));
  const favorite = uiAction(favoriteIDs.has(track.id) ? '♥ Favorite' : '♡ Favorite', async () => {
    const add = !favoriteIDs.has(track.id);
    await userAPI(`/api/v1/favorites/tracks/${track.id}`, { method: add ? 'PUT' : 'DELETE' });
    if (add) favoriteIDs.add(track.id); else favoriteIDs.delete(track.id);
    favorite.textContent = add ? '♥ Favorite' : '♡ Favorite';
    favorite.setAttribute('aria-pressed', String(add));
  });
  favorite.setAttribute('aria-pressed', String(favoriteIDs.has(track.id)));
  controls.append(favorite, uiAction('Add to playlist', async () => {
    if (!playlistTarget.value) { userStatus.textContent = 'Choose a playlist above first.'; return; }
    const playlist = await userAPI(`/api/v1/playlists/${playlistTarget.value}`);
    await userWrite('POST', `/api/v1/playlists/${playlist.id}/items`, { track_id: track.id, expected_version: playlist.revision });
    userStatus.textContent = 'Added to playlist.';
  }));
  row.append(controls);
}
function setPlayback(track, selection) {
  if (currentPlayback?.session) sendReport('stopped');
  currentPlayback = { track, selection, session: null, decoderFailed: false };
  lastClock = performance.now();
}
async function selectedQueuePlayback(expected = null) {
  const q = await refreshQueue();
  if (expected && (q.selection_state !== 'selected' || q.current_item_id !== expected.itemID || q.selection_token !== expected.token)) return;
  if (q.selection_state !== 'selected' || !q.current_item_id) return;
  const item = q.items.find(value => value.id === q.current_item_id);
  if (!item?.available) { userStatus.textContent = 'Selected Track is unavailable.'; return; }
  const track = await userAPI(`/api/v1/tracks/${item.track_id}`);
  window.resonanceBrowse.playTrack(track, { itemID: item.id, token: q.selection_token });
}
async function advanceQueue(direction, failureCode = null) {
  const q = queue || await refreshQueue();
  if (!q.items.length) return;
  if (direction === 'previous' && currentPlayback?.selection && userAudio.currentTime > 3) { userAudio.currentTime = 0; return; }
  const selection = currentPlayback?.selection;
  const currentItemID = selection ? selection.itemID : q.current_item_id;
  const selectionToken = selection ? selection.token : q.selection_token;
  const change = await userWrite('POST', '/api/v1/queue/advance', {
    direction, expected_version: q.revision, expected_current_item_id: currentItemID,
    selection_token: selectionToken, ...(failureCode ? { failure_code: failureCode } : {})
  });
  await refreshQueue();
  if (activeView === 'queue') await loadUserView(true);
  const selectionUnchanged = change?.selection_state === 'selected' && change.current_item_id === currentItemID && change.selection_token === selectionToken;
  if (!selectionUnchanged) {
    accrue();
    userAudio.pause();
    if (currentPlayback?.session) await sendReport('stopped');
  }
  if (change?.selection_state === 'selected' && change.current_item_id && change.selection_token && !selectionUnchanged)
    await selectedQueuePlayback({ itemID: change.current_item_id, token: change.selection_token });
  else if (change?.selection_state !== 'selected') userAudio.pause();
}
async function reorderQueue(q, index, delta) {
  if (index + delta < 0 || index + delta >= q.items.length) return;
  const order = q.items.map(item => item.id);
  [order[index], order[index + delta]] = [order[index + delta], order[index]];
  await userWrite('PUT', '/api/v1/queue/order', { item_ids: order, expected_version: q.revision });
  await loadUserView(true);
}
async function renderQueue() {
  const q = await refreshQueue(); userItems.replaceChildren(); userMore.hidden = true;
  for (const [index, item] of q.items.entries()) {
    const row = document.createElement('li');
    row.append(uiNode('strong', item.title || 'Untitled track'), uiNode('span', item.artist_credit || 'Unknown artist', 'item-subtitle'));
    if (item.id === q.current_item_id) row.append(uiNode('span', q.selection_state === 'selected' ? 'Selected' : 'Stopped here', 'badge'));
    if (!item.available) row.append(uiNode('span', 'Unavailable', 'badge'));
    if (item.last_skip_code) row.append(uiNode('span', `Skipped: ${item.last_skip_code}`, 'badge'));
    const buttons = uiNode('span', '', 'item-actions');
    buttons.append(uiAction('Play now', async () => addQueue(await userAPI(`/api/v1/tracks/${item.track_id}`), 'now')),
      uiAction('Remove', async () => { await userWrite('DELETE', `/api/v1/queue/items/${item.id}`, { expected_version: q.revision }); await loadUserView(true); }));
    if (index > 0) buttons.append(uiAction('↑', () => reorderQueue(q, index, -1)));
    if (index < q.items.length - 1) buttons.append(uiAction('↓', () => reorderQueue(q, index, 1)));
    row.append(buttons); userItems.append(row);
  }
  userStatus.textContent = q.items.length ? `${q.items.length} queue items · ${q.selection_state}` : 'Queue is empty';
}
async function renderPlaylists(reset) {
  if (currentPlaylist) return renderPlaylistDetail();
  if (reset) { viewCursor = null; userItems.replaceChildren(); }
  const page = await userAPI(`/api/v1/playlists?limit=50${viewCursor ? `&cursor=${encodeURIComponent(viewCursor)}` : ''}`);
  for (const playlist of page.items) {
    const row = document.createElement('li');
    row.append(uiAction(playlist.name, async () => { currentPlaylist = playlist.id; userTitle.replaceChildren(uiAction('← Back', () => selectUserView('playlists')), document.createTextNode(` ${playlist.name}`)); setControls('playlists'); await loadUserView(true); }));
    userItems.append(row);
  }
  viewCursor = page.next_cursor; userMore.hidden = !viewCursor;
  userStatus.textContent = userItems.children.length ? `${userItems.children.length} playlists shown` : 'No playlists yet';
}
async function reorderPlaylist(index, delta) {
  const order = currentPlaylist.items.map(item => item.id);
  [order[index], order[index + delta]] = [order[index + delta], order[index]];
  await userWrite('PUT', `/api/v1/playlists/${currentPlaylist.id}/order`, { item_ids: order, expected_version: currentPlaylist.revision });
  await renderPlaylistDetail();
}
async function renderPlaylistDetail() {
  const id = typeof currentPlaylist === 'string' ? currentPlaylist : currentPlaylist.id;
  const detail = await userAPI(`/api/v1/playlists/${id}`);
  currentPlaylist = detail;
  userItems.replaceChildren(); userMore.hidden = true;
  const controls = uiNode('li', '', 'view-controls');
  controls.append(uiAction('Rename', async () => {
    const name = prompt('Playlist name', detail.name); if (name === null) return;
    await userWrite('PATCH', `/api/v1/playlists/${detail.id}`, { name, expected_version: detail.revision });
    userTitle.lastChild.textContent = ` ${name}`; await renderPlaylistDetail();
  }), uiAction('Delete playlist', async () => {
    await userWrite('DELETE', `/api/v1/playlists/${detail.id}`, { expected_version: detail.revision }, false);
    await refreshPlaylists(); selectUserView('playlists');
  }));
  userItems.append(controls);
  for (const [index, item] of detail.items.entries()) {
    const row = document.createElement('li');
    row.append(uiNode('strong', item.title || 'Untitled track'), uiNode('span', item.artist_credit || 'Unknown artist', 'item-subtitle'));
    if (!item.available) row.append(uiNode('span', 'Unavailable', 'badge'));
    const buttons = uiNode('span', '', 'item-actions');
    buttons.append(uiAction('Play now', async () => addQueue(await userAPI(`/api/v1/tracks/${item.track_id}`), 'now')),
      uiAction('Remove', async () => { await userWrite('DELETE', `/api/v1/playlists/${detail.id}/items/${item.id}`, { expected_version: detail.revision }); await renderPlaylistDetail(); }));
    if (index > 0) buttons.append(uiAction('↑', () => reorderPlaylist(index, -1)));
    if (index < detail.items.length - 1) buttons.append(uiAction('↓', () => reorderPlaylist(index, 1)));
    row.append(buttons); userItems.append(row);
  }
  userStatus.textContent = `${detail.items.length} entries`;
}
async function renderFavorites(reset) {
  const generation = ++favoriteLoadGeneration;
  if (reset) { viewCursor = null; userItems.replaceChildren(); }
  const page = await userAPI(`/api/v1/favorites?limit=50${viewCursor ? `&cursor=${encodeURIComponent(viewCursor)}` : ''}`);
  if (generation !== favoriteLoadGeneration || activeView !== 'favorites') return;
  for (const item of page.items) {
    const row = document.createElement('li');
    row.append(uiAction(item.title || 'Untitled track', async () => window.resonanceBrowse.playTrack(await userAPI(`/api/v1/tracks/${item.track_id}`))),
      uiNode('span', item.artist_credit || 'Unknown artist', 'item-subtitle'));
    if (!item.available) row.append(uiNode('span', 'Unavailable', 'badge'));
    row.append(uiAction('Remove favorite', async () => { await userAPI(`/api/v1/favorites/tracks/${item.track_id}`, { method: 'DELETE' }); favoriteIDs.delete(item.track_id); await renderFavorites(true); }));
    userItems.append(row);
  }
  viewCursor = page.next_cursor; userMore.hidden = !viewCursor;
  userStatus.textContent = userItems.children.length ? `${userItems.children.length} favorites shown` : 'No favorites yet';
}
async function renderHistory(reset) {
  if (reset) { viewCursor = null; userItems.replaceChildren(); }
  const page = await userAPI(`/api/v1/history?limit=50${viewCursor ? `&cursor=${encodeURIComponent(viewCursor)}` : ''}`);
  for (const item of page.items) {
    const row = document.createElement('li');
    row.append(uiNode('strong', item.title || 'Untitled track'),
      uiNode('span', `${Math.round(item.listened_ms / 1000)}s listened · ${item.completed_at ? 'Completed' : 'Meaningful'}`, 'item-subtitle'));
    userItems.append(row);
  }
  viewCursor = page.next_cursor; userMore.hidden = !viewCursor;
  userStatus.textContent = userItems.children.length ? `${userItems.children.length} history entries shown` : 'No qualifying listens yet';
}
async function loadUserView(reset = false) {
  if (!activeView) return;
  userStatus.textContent = 'Loading…'; userMore.hidden = true;
  try {
    if (activeView === 'queue') await renderQueue();
    if (activeView === 'playlists') await renderPlaylists(reset);
    if (activeView === 'favorites') await renderFavorites(reset);
    if (activeView === 'history') await renderHistory(reset);
  } catch (error) { userFailure(error); }
}

function accrue() {
  const now = performance.now();
  if (currentPlayback?.session && !userAudio.paused && !userAudio.seeking && !stalled && userAudio.readyState >= 3)
    currentPlayback.session.listenedMS += Math.min(2000, Math.max(0, now - lastClock));
  lastClock = now;
}
function reportBody(reason) {
  const session = currentPlayback.session;
  const duration = Number.isFinite(userAudio.duration) && userAudio.duration > 0 ? Math.round(userAudio.duration * 1000) : null;
  return { sequence: ++session.sequence, listened_ms: Math.round(session.listenedMS), position_ms: Math.max(0, Math.round(userAudio.currentTime * 1000)), duration_ms: duration, seek_count: session.seekCount, ...(reason ? { terminal_reason: reason } : {}) };
}
async function sendReport(reason = null) {
  accrue(); if (!currentPlayback?.session) return;
  const session = currentPlayback.session; const report = reportBody(reason);
  const pending = JSON.stringify({ id: session.id, report });
  sessionStorage.setItem('resonance.pending_report', pending);
  try {
    await userWrite('PUT', `/api/v1/listening-sessions/${session.id}/report`, report, false);
    if (sessionStorage.getItem('resonance.pending_report') === pending) sessionStorage.removeItem('resonance.pending_report');
    if (reason && currentPlayback?.session === session) currentPlayback.session = null;
  } catch (error) { if (reason) userFailure(error); }
}
async function startSession() {
  if (starting || !currentPlayback || currentPlayback.session || userAudio.paused || userAudio.currentTime <= 0.1) return;
  starting = true; const playback = currentPlayback;
  try {
    const started = await userWrite('POST', '/api/v1/listening-sessions', {
      id: crypto.randomUUID(), track_id: playback.track.id, client_instance_id: instanceID,
      ...(playback.selection ? { queue_item_id: playback.selection.itemID, selection_token: playback.selection.token } : {})
    }, false);
    if (currentPlayback === playback && !userAudio.error) { playback.session = { id: started.id, sequence: 0, listenedMS: 0, seekCount: 0 }; lastClock = performance.now(); }
  } catch { userError.textContent = 'Audio is playing, but listening history could not start.'; userError.hidden = false; }
  finally { starting = false; }
}
async function ended() {
  accrue(); const playback = currentPlayback;
  if (!playback?.session) return;
  if (!playback.selection) { await sendReport('ended'); return; }
  const q = await refreshQueue();
  const request = { direction: 'ended', expected_version: q.revision, expected_current_item_id: playback.selection.itemID,
    selection_token: playback.selection.token, session_id: playback.session.id, final_report: reportBody('ended') };
  const pending = { key: crypto.randomUUID(), request };
  sessionStorage.removeItem('resonance.pending_report');
  sessionStorage.setItem('resonance.pending_ended', JSON.stringify(pending));
  try {
    const change = await postPendingEnded(pending);
    sessionStorage.removeItem('resonance.pending_ended'); playback.session = null;
    await refreshQueue(); if (activeView === 'queue') await loadUserView(true);
    if (change?.selection_state === 'selected' && change.current_item_id && change.selection_token)
      await selectedQueuePlayback({ itemID: change.current_item_id, token: change.selection_token });
  } catch (error) {
    if (error.code === 'stale_selection') sessionStorage.removeItem('resonance.pending_ended');
    userError.textContent = error.code === 'stale_selection' ? 'Queue selection changed in another tab. This track did not advance the queue.' : 'Queue advance was not saved. Retry after the server recovers.';
    userError.hidden = false;
  }
}
async function postPendingEnded(pending) {
  const send = item => userAPI('/api/v1/queue/advance', { method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': item.key }, body: JSON.stringify(item.request) });
  try { return await send(pending); }
  catch (error) {
    if (error.code !== 'stale_version') throw error;
    const q = await userAPI('/api/v1/queue');
    if (q.selection_state !== 'selected' || q.current_item_id !== pending.request.expected_current_item_id || q.selection_token !== pending.request.selection_token) {
      throw { code: 'stale_selection', status: 409 };
    }
    const rebased = { key: crypto.randomUUID(), request: { ...pending.request, expected_version: q.revision } };
    sessionStorage.setItem('resonance.pending_ended', JSON.stringify(rebased));
    return send(rebased);
  }
}
async function retryPending() {
  for (const [name, path, method] of [['resonance.pending_ended', '/api/v1/queue/advance', 'POST'], ['resonance.pending_report', null, 'PUT']]) {
    const raw = sessionStorage.getItem(name); if (!raw) continue;
    try {
      const pending = JSON.parse(raw);
      if (name === 'resonance.pending_ended') await postPendingEnded(pending);
      else await userAPI(path || `/api/v1/listening-sessions/${pending.id}/report`, { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(pending.report) });
      sessionStorage.removeItem(name);
    } catch (error) {
      if (error.status >= 400 && error.status < 500) sessionStorage.removeItem(name);
    }
  }
  for (const pending of pendingMutations()) {
    try {
      await userAPI(pending.path, { method: pending.method, headers: { 'Content-Type': 'application/json', 'Idempotency-Key': pending.key }, body: pending.body });
      removePendingMutation(pending.identity, pending.key);
    } catch (error) {
      if (error.status >= 400 && error.status < 500) removePendingMutation(pending.identity, pending.key);
    }
  }
  try { await refreshQueue(); } catch { /* Durable queue reads remain available after recovery. */ }
}

window.resonanceUser = { selectView: selectUserView, onBrowseView, decorateTrackRow, setPlayback };
for (const id of ['next', 'player-next']) document.querySelector(`#${id}`).addEventListener('click', () => advanceQueue('next', currentPlayback?.decoderFailed ? 'resolver_failed' : null).catch(userFailure));
for (const id of ['previous', 'player-previous']) document.querySelector(`#${id}`).addEventListener('click', () => advanceQueue('previous').catch(userFailure));
document.querySelector('#clear-queue').addEventListener('click', async () => {
  try { const q = await refreshQueue(); await userWrite('DELETE', '/api/v1/queue', { expected_version: q.revision }); userAudio.pause(); await loadUserView(true); }
  catch (error) { userFailure(error); }
});
playlistCreate.addEventListener('submit', async event => {
  event.preventDefault();
  try { await userWrite('POST', '/api/v1/playlists', { name: document.querySelector('#playlist-name').value, expected_version: 0 }); document.querySelector('#playlist-name').value = ''; await refreshPlaylists(); await loadUserView(true); }
  catch (error) { userFailure(error); }
});
userMore.addEventListener('click', event => {
  if (!activeView || !['playlists', 'favorites', 'history'].includes(activeView)) return;
  event.stopImmediatePropagation(); loadUserView(false);
}, true);
userAudio.addEventListener('timeupdate', () => { accrue(); startSession(); });
userAudio.addEventListener('seeking', () => { if (currentPlayback?.session) currentPlayback.session.seekCount++; accrue(); });
userAudio.addEventListener('seeked', () => { if (currentPlayback?.session) sendReport(); });
userAudio.addEventListener('waiting', () => { stalled = true; accrue(); });
userAudio.addEventListener('playing', () => { stalled = false; lastClock = performance.now(); });
userAudio.addEventListener('pause', accrue);
userAudio.addEventListener('ended', () => ended().catch(userFailure));
userAudio.addEventListener('error', () => { if (currentPlayback) currentPlayback.decoderFailed = true; if (currentPlayback?.session) sendReport('decoder_error'); });
setInterval(() => { accrue(); if (currentPlayback?.session && !userAudio.paused && currentPlayback.session.listenedMS >= (currentPlayback.session.lastSentMS || 0) + 10000) { currentPlayback.session.lastSentMS = currentPlayback.session.listenedMS; sendReport(); } }, 500);
window.addEventListener('pagehide', () => { if (currentPlayback?.session) sendReport('disconnected'); });
retryPending().then(() => Promise.allSettled([refreshFavorites(), refreshPlaylists()])).then(() => {
  if (!activeView && document.querySelector('nav button[data-view="tracks"]')?.getAttribute('aria-current') === 'page') window.resonanceBrowse.load(true);
});
