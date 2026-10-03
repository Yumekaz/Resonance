(function (root) {
  function create({ read, set, changed = () => {} }) {
    const values = new Map(),
      versions = new Map(),
      mutations = new Map(),
      waiting = new Map();
    let scheduled = false;
    const get = (id) => ({
      known: values.has(id),
      present: values.get(id) === true,
    });
    const publish = (id, present) => {
      values.set(id, present);
      changed({ id, present });
    };
    async function load(ids) {
      const unique = [...new Set(ids)];
      for (let start = 0; start < unique.length; start += 200) {
        const batch = unique.slice(start, start + 200);
        const observed = new Map(
          batch.map((id) => [id, versions.get(id) || 0]),
        );
        const found = new Set(await read(batch));
        for (const id of batch)
          if (
            !mutations.has(id) &&
            observed.get(id) === (versions.get(id) || 0)
          )
            publish(id, found.has(id));
      }
    }
    function ensure(id) {
      if (values.has(id)) return Promise.resolve(get(id));
      if (waiting.has(id)) return waiting.get(id).promise;
      let resolve, reject;
      const promise = new Promise((yes, no) => {
        resolve = yes;
        reject = no;
      });
      waiting.set(id, { promise, resolve, reject });
      if (!scheduled) {
        scheduled = true;
        queueMicrotask(async () => {
          scheduled = false;
          const batch = [...waiting.entries()].filter(
            ([, pending]) => !pending.running,
          );
          for (const [, pending] of batch) pending.running = true;
          try {
            await load(batch.map(([id]) => id));
            for (const [id, pending] of batch) pending.resolve(get(id));
          } catch (error) {
            for (const [, pending] of batch) pending.reject(error);
          } finally {
            for (const [id, pending] of batch)
              if (waiting.get(id) === pending) waiting.delete(id);
          }
        });
      }
      return promise;
    }
    async function save(id, present) {
      if (mutations.has(id)) return mutations.get(id);
      versions.set(id, (versions.get(id) || 0) + 1);
      const operation = Promise.resolve()
        .then(() => set(id, present))
        .then(() => {
          publish(id, present);
          return present;
        });
      mutations.set(id, operation);
      try {
        return await operation;
      } finally {
        if (mutations.get(id) === operation) mutations.delete(id);
      }
    }
    return {
      get,
      load,
      ensure,
      save,
      seed(id, present) {
        if (mutations.has(id)) return;
        versions.set(id, (versions.get(id) || 0) + 1);
        publish(id, present);
      },
      async toggle(id) {
        await ensure(id);
        return save(id, !get(id).present);
      },
    };
  }
  if (typeof module !== "undefined") module.exports = { create };
  if (root) root.resonanceCreateFavorites = create;
})(typeof window === "undefined" ? null : window);
