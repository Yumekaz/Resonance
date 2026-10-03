// Reorder motion explains an acknowledged change. Keyboard and reduced-motion
// users receive focus and a status announcement without a visual delay.
window.resonanceRows = {
  capture(host) {
    return new Map(
      [...host.querySelectorAll("[data-item-id]")].map((row) => [
        row.dataset.itemId,
        row.getBoundingClientRect().top,
      ]),
    );
  },
  settle(host, before, itemID) {
    const row = [...host.querySelectorAll("[data-item-id]")].find(
      (row) => row.dataset.itemId === itemID,
    );
    if (!document.querySelector(".context-menu"))
      row
        ?.querySelector(".item-actions button")
        ?.focus({ preventScroll: true });
    if (
      matchMedia("(prefers-reduced-motion: reduce)").matches ||
      document.body.dataset.input === "keyboard"
    )
      return;
    for (const item of host.querySelectorAll("[data-item-id]")) {
      const top = before.get(item.dataset.itemId),
        bounds = item.getBoundingClientRect();
      if (top === undefined || bounds.bottom < 0 || bounds.top > innerHeight)
        continue;
      const offset = top - bounds.top;
      if (!offset || Math.abs(offset) > innerHeight) continue;
      item
        .animate(
          [{ transform: `translateY(${offset}px)` }, { transform: "none" }],
          { duration: 160, easing: "cubic-bezier(.23,1,.32,1)" },
        )
        .finished.catch(() => {});
    }
  },
};
