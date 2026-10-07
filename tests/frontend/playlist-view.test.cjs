const { test } = require("node:test");
const assert = require("node:assert/strict");
const { project } = require("../../web/playlist-view.js");
const rows = [
  {
    id: "one",
    position: 0,
    track_id: "same",
    title: "Same",
    artist_credit: "Z",
    album_title: "First",
    added_at: "2026-01-01T00:00:00Z",
  },
  {
    id: "two",
    position: 1,
    track_id: "same",
    title: "Same",
    artist_credit: "A",
    album_title: "Other",
    added_at: "2026-01-02T00:00:00Z",
  },
  {
    id: "three",
    position: 2,
    title: "Écho",
    artist_credit: null,
    album_title: null,
    added_at: "2026-01-03T00:00:00Z",
  },
];
test("playlist view preserves occurrence identity and saved order while sorting/filtering", () => {
  const original = JSON.stringify(rows);
  assert.deepEqual(
    project(rows, "", "artist").map((row) => row.id),
    ["two", "three", "one"],
  );
  assert.deepEqual(
    project(rows, "same", "title_desc").map((row) => row.id),
    ["one", "two"],
  );
  assert.deepEqual(
    project(rows, "Other", "original").map((row) => row.id),
    ["two"],
  );
  assert.deepEqual(
    project(rows, "E\u0301cho", "original").map((row) => row.id),
    ["three"],
  );
  assert.deepEqual(
    project(rows, "", "recent").map((row) => row.id),
    ["three", "two", "one"],
  );
  assert.equal(JSON.stringify(rows), original);
});
test("playlist title order matches Unicode code points and stable duplicate positions", () => {
  const items = [
    { id: "supplementary", position: 0, title: "𐀀" },
    { id: "bmp", position: 1, title: "\uE000" },
    { id: "empty", position: 2, title: "" },
  ];
  assert.deepEqual(
    project(items, "", "title").map((row) => row.id),
    ["empty", "bmp", "supplementary"],
  );
});
