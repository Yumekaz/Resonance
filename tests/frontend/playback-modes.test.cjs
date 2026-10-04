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

test("continuous intent is explicit and repeat-one wins only at natural ending", () => {
  const { advanceOptions } = require("../../web/playback-modes.js");
  assert.deepEqual(
    advanceOptions(
      { repeat: "off", continuous: true, shuffle: false },
      "ended",
    ),
    { repeat: "all", reshuffle: false },
  );
  assert.deepEqual(
    advanceOptions(
      { repeat: "off", continuous: false, shuffle: true },
      "ended",
    ),
    { repeat: "off", reshuffle: false },
  );
  assert.deepEqual(
    advanceOptions({ repeat: "one", continuous: true, shuffle: true }, "ended"),
    { repeat: "one", reshuffle: false },
  );
  assert.deepEqual(
    advanceOptions({ repeat: "one", continuous: true, shuffle: true }, "next"),
    { repeat: "all", reshuffle: true },
  );
  assert.deepEqual(
    advanceOptions(
      { repeat: "all", continuous: false, shuffle: true },
      "previous",
    ),
    { repeat: "all", reshuffle: false },
  );
});
