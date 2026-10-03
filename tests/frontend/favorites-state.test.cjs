const { test } = require("node:test");
const assert = require("node:assert/strict");
const { create } = require("../../web/favorites-state.js");
test("visible membership checks coalesce instead of traversing saved favorites", async () => {
  const batches = [];
  const state = create({
    read: async (ids) => {
      batches.push(ids);
      return ["a"];
    },
    set: async () => {},
  });
  await Promise.all(["a", "b", "c"].map((id) => state.ensure(id)));
  assert.deepEqual(batches, [["a", "b", "c"]]);
  assert.deepEqual(state.get("a"), { known: true, present: true });
  assert.deepEqual(state.get("b"), { known: true, present: false });
});
test("an older membership response cannot overwrite an acknowledged favorite mutation", async () => {
  let release;
  const state = create({
    read: () =>
      new Promise((r) => {
        release = r;
      }),
    set: async () => {},
  });
  state.seed("a", false);
  const old = state.load(["a"]);
  await state.save("a", true);
  release([]);
  await old;
  assert.equal(state.get("a").present, true);
});
test("failed writes retain the last acknowledged value and finite reads stay bounded", async () => {
  const sizes = [];
  const state = create({
    read: async (ids) => {
      sizes.push(ids.length);
      return [];
    },
    set: async () => {
      throw Error("offline");
    },
  });
  await state.load(Array.from({ length: 450 }, (_, i) => String(i)));
  assert.deepEqual(sizes, [200, 200, 50]);
  await assert.rejects(state.toggle("0"), /offline/);
  assert.equal(state.get("0").present, false);
});
