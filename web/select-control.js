// Progressive enhancement: native values/change events remain authoritative.
// Share the existing menu's positioning, dismissal and keyboard navigation.
(() => {
  const controls = new WeakMap();
  function enhance(select) {
    if (controls.has(select) || select.multiple || select.size > 1) return;
    const trigger = document.createElement("button");
    trigger.type = "button";
    trigger.className = "select-trigger";
    trigger.id = select.id ? `${select.id}-trigger` : "";
    trigger.setAttribute("aria-haspopup", "menu");
    trigger.setAttribute("aria-expanded", "false");
    const label =
      select.getAttribute("aria-label") ||
      select.labels?.[0]?.textContent ||
      "Choose an option";
    const text = document.createElement("span");
    const caret = window.resonanceUI.icon("caret-down");
    trigger.append(text, caret);
    select.after(trigger);
    select.hidden = true;
    select.tabIndex = -1;
    for (const associated of select.labels || [])
      associated.htmlFor = trigger.id;
    const refresh = () => {
      if (trigger.getAttribute("aria-expanded") === "true")
        window.resonanceMenus.close();
      const option = select.selectedOptions[0];
      text.textContent = option?.textContent || "Choose an option";
      trigger.setAttribute("aria-label", `${label}: ${text.textContent}`);
      trigger.disabled = select.disabled || !select.options.length;
      trigger.hidden = !select.isConnected;
    };
    controls.set(select, { refresh });
    function open() {
      if (trigger.getAttribute("aria-expanded") === "true") {
        window.resonanceMenus.close();
        return;
      }
      const surface = window.resonanceMenus.open(trigger, label);
      surface.content.classList.add("select-menu");
      for (const option of select.options) {
        if (option.hidden) continue;
        const choice = document.createElement("button");
        choice.type = "button";
        choice.setAttribute("role", "menuitemradio");
        choice.setAttribute("aria-checked", String(option.selected));
        choice.disabled = option.disabled;
        choice.append(
          window.resonanceUI.icon("check"),
          document.createTextNode(option.textContent),
        );
        choice.addEventListener("click", () => {
          const changed = select.value !== option.value;
          surface.close();
          select.value = option.value;
          refresh();
          if (changed)
            select.dispatchEvent(new Event("change", { bubbles: true }));
        });
        surface.content.append(choice);
      }
      surface.ready();
    }
    trigger.addEventListener("click", open);
    trigger.addEventListener("keydown", (event) => {
      if (["ArrowDown", "ArrowUp"].includes(event.key)) {
        event.preventDefault();
        open();
      }
    });
    select.addEventListener("change", refresh);
    refresh();
  }
  for (const select of document.querySelectorAll("select")) enhance(select);
  new MutationObserver((records) => {
    for (const record of records) {
      const select = record.target.closest?.("select");
      if (select) controls.get(select)?.refresh();
      for (const added of record.addedNodes) {
        if (added.nodeType !== Node.ELEMENT_NODE) continue;
        if (added.matches("select")) enhance(added);
        for (const select of added.querySelectorAll("select")) enhance(select);
      }
    }
  }).observe(document.body, {
    childList: true,
    subtree: true,
    attributes: true,
    attributeFilter: ["disabled", "selected"],
  });
})();
