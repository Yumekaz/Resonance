// A contextual action surface, distinct from dialogs that collect input.
(() => {
  let active = null;
  function close(returnFocus = true) {
    if (!active) return false;
    const { menu, anchor } = active;
    active = null;
    menu.remove();
    anchor.setAttribute("aria-expanded", "false");
    if (returnFocus && anchor.isConnected)
      anchor.focus({ preventScroll: true });
    return true;
  }
  function open(anchor, title) {
    close(false);
    const menu = document.createElement("div");
    menu.className = "context-menu";
    menu.setAttribute("role", "menu");
    menu.setAttribute("aria-label", title);
    const heading = document.createElement("p");
    heading.className = "menu-heading";
    heading.textContent = title;
    menu.append(heading);
    // Place within the dialog's top layer when opened from Now Playing.
    (anchor.closest("dialog") || document.body).append(menu);
    anchor.setAttribute("aria-haspopup", "menu");
    anchor.setAttribute("aria-expanded", "true");
    active = { menu, anchor };
    function position() {
      if (!menu.isConnected) return;
      if (!anchor.isConnected) {
        close(false);
        return;
      }
      const bounds = anchor.getBoundingClientRect();
      const viewport = window.visualViewport;
      const top = viewport?.offsetTop || 0,
        left = viewport?.offsetLeft || 0;
      const height = viewport?.height || innerHeight,
        visibleWidth = viewport?.width || innerWidth;
      if (bounds.bottom < top || bounds.top > top + height) {
        close(false);
        return;
      }
      const selectMenu = menu.classList.contains("select-menu");
      const width = Math.min(
        selectMenu ? Math.max(200, Math.min(300, bounds.width)) : 300,
        visibleWidth - 24,
      );
      menu.style.width = `${width}px`;
      menu.style.left = `${Math.max(left + 12, Math.min(selectMenu ? bounds.left : bounds.right - width, left + visibleWidth - width - 12))}px`;
      const below = top + height - bounds.bottom - 18;
      const above = bounds.top - top - 18;
      const useBelow = below >= menu.offsetHeight || below >= above;
      menu.style.maxHeight = `${Math.max(44, useBelow ? below : above)}px`;
      menu.style.top = `${useBelow ? bounds.bottom + 6 : Math.max(top + 12, bounds.top - menu.offsetHeight - 6)}px`;
      menu.dataset.placement = useBelow ? "below" : "above";
    }
    active.position = position;
    menu.addEventListener("keydown", (event) => {
      const buttons = [...menu.querySelectorAll("button:not(:disabled)")];
      const index = buttons.indexOf(document.activeElement);
      if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
        event.preventDefault();
        const next =
          event.key === "Home"
            ? 0
            : event.key === "End"
              ? buttons.length - 1
              : (index +
                  (event.key === "ArrowDown" ? 1 : -1) +
                  buttons.length) %
                buttons.length;
        buttons[next]?.focus();
      } else if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        close();
      } else if (event.key === "Tab") close();
      else if (
        event.key.length === 1 &&
        !event.ctrlKey &&
        !event.metaKey &&
        !event.altKey
      ) {
        const match = buttons
          .slice(index + 1)
          .concat(buttons.slice(0, index + 1))
          .find((button) =>
            button.textContent
              .trim()
              .toLocaleLowerCase()
              .startsWith(event.key.toLocaleLowerCase()),
          );
        if (match) {
          event.preventDefault();
          match.focus();
        }
      }
    });
    let ready = false;
    function prepare() {
      if (active?.menu !== menu) return;
      ready = true;
      for (const button of menu.querySelectorAll("button"))
        if (!button.hasAttribute("role"))
          button.setAttribute("role", "menuitem");
      position();
      (
        menu.querySelector('button[aria-checked="true"]:not(:disabled)') ||
        menu.querySelector("button:not(:disabled)")
      )?.focus({ preventScroll: true });
    }
    queueMicrotask(() => {
      if (ready || active?.menu !== menu) return;
      prepare();
    });
    return {
      content: menu,
      ready: prepare,
      close: () => {
        if (active?.menu === menu) return close();
        return false;
      },
    };
  }
  document.addEventListener("pointerdown", (event) => {
    if (
      active &&
      !active.menu.contains(event.target) &&
      !active.anchor.contains(event.target)
    )
      close(false);
  });
  window.addEventListener("resize", () => active?.position());
  window.visualViewport?.addEventListener("resize", () => active?.position());
  document.addEventListener(
    "scroll",
    (event) => {
      if (active && !active.menu.contains(event.target)) active.position();
    },
    true,
  );
  window.addEventListener("popstate", () => close(false));
  window.resonanceMenus = { open, close, hasOpen: () => !!active };
})();
