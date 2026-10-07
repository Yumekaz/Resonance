(function (root) {
  const orders = [
    "original",
    "title",
    "title_desc",
    "artist",
    "album",
    "recent",
  ];
  const text = (value) => (value || "").normalize("NFC").toLowerCase();
  function project(items, query = "", order = "original") {
    const needle = text(query.trim());
    const found = items.filter(
      (item) =>
        !needle ||
        [
          item.title || "Untitled track",
          item.artist_credit || "Unknown artist",
          item.album_title || "",
        ].some((value) => text(value).includes(needle)),
    );
    // Match the source snapshot's explicit C collation, including supplementary
    // Unicode characters; JS's default comparison orders UTF-16 code units.
    const compare = (a, b) => {
      const left = Array.from(a),
        right = Array.from(b);
      for (let i = 0; i < Math.min(left.length, right.length); i++) {
        const delta = left[i].codePointAt(0) - right[i].codePointAt(0);
        if (delta) return Math.sign(delta);
      }
      return Math.sign(left.length - right.length);
    };
    if (order !== "original")
      found.sort((a, b) => {
        let result = 0;
        if (order === "recent")
          result = compare(b.added_at || "", a.added_at || "");
        else {
          const field =
            order === "artist"
              ? "artist_credit"
              : order === "album"
                ? "album_title"
                : "title";
          const fallback =
            field === "title"
              ? "Untitled track"
              : field === "artist_credit"
                ? "Unknown artist"
                : "";
          result = compare(
            text(a[field] || fallback),
            text(b[field] || fallback),
          );
          if (!result && field !== "title")
            result = compare(
              text(a.title || "Untitled track"),
              text(b.title || "Untitled track"),
            );
          if (order === "title_desc") result = -result;
        }
        return result || a.position - b.position || compare(a.id, b.id);
      });
    return found;
  }
  if (typeof module !== "undefined") module.exports = { project };
  if (!root?.document) return;
  let order = "original",
    density = "comfortable";
  try {
    const saved = JSON.parse(
      localStorage.getItem("resonance-collection-view-v1"),
    );
    if (orders.includes(saved?.order)) order = saved.order;
    if (["compact", "comfortable"].includes(saved?.density))
      density = saved.density;
  } catch {}
  function set(values) {
    if (orders.includes(values.order)) order = values.order;
    if (["compact", "comfortable"].includes(values.density))
      density = values.density;
    root.document.body.dataset.density = density;
    try {
      localStorage.setItem(
        "resonance-collection-view-v1",
        JSON.stringify({ order, density }),
      );
    } catch {}
  }
  set({});
  root.resonancePlaylistView = {
    project,
    get: () => ({ order, density }),
    set,
  };
})(typeof window === "undefined" ? null : window);
