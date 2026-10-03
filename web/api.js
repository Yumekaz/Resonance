(() => {
  const pendingMutationStorageKey = "resonance.pending_mutations";

  function pendingMutations() {
    try {
      const value = JSON.parse(
        sessionStorage.getItem(pendingMutationStorageKey) || "[]",
      );
      return Array.isArray(value)
        ? value.filter(
            (item) =>
              item &&
              typeof item.identity === "string" &&
              typeof item.key === "string" &&
              typeof item.method === "string" &&
              typeof item.path === "string" &&
              typeof item.body === "string",
          )
        : [];
    } catch {
      return [];
    }
  }
  function savePendingMutations(items) {
    sessionStorage.setItem(pendingMutationStorageKey, JSON.stringify(items));
  }
  function removePendingMutation(identity, key) {
    savePendingMutations(
      pendingMutations().filter(
        (item) => item.identity !== identity || item.key !== key,
      ),
    );
  }
  function mutationIdentity(method, path, body) {
    const intent = body && typeof body === "object" ? { ...body } : body;
    if (intent && typeof intent === "object") delete intent.expected_version;
    return `${method}\n${path}\n${JSON.stringify(intent)}`;
  }

  async function userAPI(path, options = {}) {
    let response;
    try {
      response = await fetch(path, { cache: "no-store", ...options });
    } catch (error) {
      if (error.name === "AbortError") throw error;
      throw { code: "catalog_unavailable" };
    }
    if (response.status === 204) return null;
    const body = await response.json().catch(() => null);
    if (!response.ok)
      throw {
        code: body?.error?.code || "catalog_unavailable",
        status: response.status,
      };
    return body;
  }
  async function userWrite(method, path, body, receipt = true) {
    const serialized = JSON.stringify(body);
    if (!receipt)
      return userAPI(path, {
        method,
        headers: { "Content-Type": "application/json" },
        body: serialized,
      });
    const identity = mutationIdentity(method, path, body);
    const pending = pendingMutations();
    let saved = pending.find((item) => item.identity === identity);
    if (!saved) {
      if (pending.length >= 16) throw { code: "catalog_unavailable" };
      saved = {
        identity,
        key: window.resonanceID(),
        method,
        path,
        body: serialized,
      };
      pending.push(saved);
      savePendingMutations(pending);
    }
    try {
      const result = await userAPI(path, {
        method,
        headers: {
          "Content-Type": "application/json",
          "Idempotency-Key": saved.key,
        },
        body: saved.body,
      });
      removePendingMutation(identity, saved.key);
      return result;
    } catch (error) {
      if (error.status >= 400 && error.status < 500)
        removePendingMutation(identity, saved.key);
      throw error;
    }
  }
  window.resonanceAPI = {
    read: userAPI,
    write: userWrite,
    pendingMutations,
    removePendingMutation,
  };
})();
