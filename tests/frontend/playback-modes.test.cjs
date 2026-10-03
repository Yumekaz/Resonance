const { test } = require("node:test");
const assert = require("node:assert/strict");
const {
  upcomingOrder,
  restoredOrder,
  shuffled,
} = require("../../web/playback-modes.js");
test("shuffle preserves current/history and distinct duplicate occurrences", () => {
  const q = {
    current_item_id: "b",
    items: ["a", "b", "c", "d", "e"].map((id) => ({
      id,
      track_id: "same-song",
    })),
  };
  const order = upcomingOrder(q, () => 0);
  assert.deepEqual(order.slice(0, 2), ["a", "b"]);
  assert.notDeepEqual(order.slice(2), ["c", "d", "e"]);
  assert.deepEqual([...order].sort(), ["a", "b", "c", "d", "e"]);
  assert.deepEqual(
    restoredOrder({ ...q, items: order.map((id) => ({ id })) }, [
      "a",
      "b",
      "c",
      "d",
      "e",
    ]),
    ["a", "b", "c", "d", "e"],
  );
});
test("shuffle off preserves new Play next slots and never restores deleted items", () => {
  const q = {
    current_item_id: "b",
    items: ["a", "b", "new", "e", "c"].map((id) => ({ id })),
  };
  assert.deepEqual(restoredOrder(q, ["a", "b", "c", "d", "e"]), [
    "a",
    "b",
    "new",
    "c",
    "e",
  ]);
  assert.deepEqual(
    upcomingOrder({ current_item_id: null, items: [] }, () => 0),
    [],
  );
  assert.deepEqual(shuffled(["only"]), ["only"]);
});
