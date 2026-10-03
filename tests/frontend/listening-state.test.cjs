const { test } = require("node:test");
const assert = require("node:assert/strict");
const { project, controls } = require("../../web/listening-state.js");

test("transport boundaries never advertise an empty Next or drop playback authority", () => {
  const q = {
    items: [{ id: "one", available: true }],
    current_item_id: "one",
    selection_token: "token",
    selection_state: "selected",
  };
  const playback = {
    track: { id: "song" },
    selection: { itemID: "one", token: "token" },
  };
  assert.deepEqual(controls(q, playback, null, 1, "off"), {
    previous: false,
    next: false,
  });
  assert.deepEqual(controls(q, playback, null, 4, "off"), {
    previous: true,
    next: false,
  });
  assert.deepEqual(controls(q, playback, null, 1, "all"), {
    previous: true,
    next: true,
  });
  assert.deepEqual(
    controls({ ...q, selection_token: "new" }, playback, null, 4, "all"),
    { previous: false, next: false },
  );
});
test("natural queue completion is distinct from a queue changed while audio keeps playing", () => {
  const stopped = {
    items: [{ id: "last" }],
    current_item_id: "last",
    selection_state: "stopped",
    selection_token: null,
  };
  const playback = {
    track: { id: "song" },
    selection: { itemID: "last", token: "old" },
    ended: true,
  };
  assert.equal(project(stopped, playback).mode, "finished");
  assert.equal(
    project(stopped, { ...playback, ended: false }).mode,
    "detached",
  );
});
const items = [{ id: "a" }, { id: "b" }, { id: "c" }];
const queue = {
  items,
  current_item_id: "b",
  selection_token: "observed",
  selection_state: "selected",
};
test("only the observed item and token couple live playback to Up Next", () => {
  assert.equal(
    project(queue, { track: {}, selection: { itemID: "b", token: "observed" } })
      .mode,
    "queue",
  );
  assert.equal(
    project(queue, { track: {}, selection: { itemID: "b", token: "older" } })
      .mode,
    "detached",
  );
  assert.equal(
    project(queue, { track: {}, selection: { itemID: "a", token: "observed" } })
      .mode,
    "detached",
  );
});
test("single-song listening cannot be presented as the saved queue selection", () => {
  const state = project(queue, { track: { id: "other" }, selection: null });
  assert.equal(state.mode, "single");
  assert.equal(state.followsQueue, false);
  assert.deepEqual(state.previous, [items[0]]);
  assert.deepEqual(state.upcoming, [items[2]]);
});
test("stopped queue and empty queue never claim actual playback", () => {
  assert.equal(
    project({ ...queue, selection_state: "stopped" }, null).mode,
    "ready",
  );
  assert.deepEqual(project(null, null).upcoming, []);
});
