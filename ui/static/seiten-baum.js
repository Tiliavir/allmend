// Drag and drop for the page tree. Without JS the tree is a plain list and
// pages are moved on their "Verschieben" page; this only adds a shortcut that
// submits the same form. Mouse: drag a row. Touch: press and hold, then drag.
// Drop on the top quarter of a row = before it, bottom quarter = after it,
// middle = as its last subpage. Escape cancels.
const tree = document.getElementById("seitenbaum");
const form = document.getElementById("verschieben");
if (tree && form) {
  const HOLD_MS = 350;
  const MOVE_PX = 6;
  let drag = null; // { li, id, pointer, x, y, active, timer }
  let target = null; // { li, zone: "before" | "after" | "into" }

  const clear = () => {
    for (const el of tree.querySelectorAll(".drop-before, .drop-after, .drop-into, .gezogen")) {
      el.classList.remove("drop-before", "drop-after", "drop-into", "gezogen");
    }
    tree.classList.remove("zieht");
    target = null;
  };

  const stop = () => {
    if (drag?.timer) clearTimeout(drag.timer);
    clear();
    drag = null;
  };

  const rowAt = (x, y) => {
    const row = document.elementFromPoint(x, y)?.closest("#seitenbaum .zeile");
    return row ? row.parentElement : null;
  };

  // Where would the page go when dropped at (x, y)? Never into itself.
  const locate = (x, y) => {
    const li = rowAt(x, y);
    if (!li || drag.li.contains(li)) return null;
    const r = li.querySelector(".zeile").getBoundingClientRect();
    const f = (y - r.top) / r.height;
    return { li, zone: f < 0.25 ? "before" : f > 0.75 ? "after" : "into" };
  };

  const show = (t) => {
    for (const el of tree.querySelectorAll(".drop-before, .drop-after, .drop-into")) {
      el.classList.remove("drop-before", "drop-after", "drop-into");
    }
    target = t;
    if (t) t.li.classList.add(`drop-${t.zone}`);
  };

  // Translate a drop into the form value "parent/before" the server expects.
  const place = (t) => {
    const { li, zone } = t;
    if (zone === "into") return `${li.dataset.id}/`;
    const parent = li.parentElement.closest("li")?.dataset.id ?? "";
    // A page whose real parent is hidden from this reader sits at the top
    // level here; the server only knows its real parent, so go last instead.
    if (li.dataset.parent !== parent) return `${parent}/`;
    if (zone === "before") return `${parent}/${li.dataset.id}`;
    const next = li.nextElementSibling;
    return `${parent}/${next && next.dataset.parent === parent ? next.dataset.id : ""}`;
  };

  // The value that describes where the page is now, to skip no-op drops.
  const current = (li) => {
    const parent = li.parentElement.closest("li")?.dataset.id ?? "";
    const next = li.nextElementSibling;
    return `${parent}/${next ? next.dataset.id : ""}`;
  };

  const begin = () => {
    drag.active = true;
    drag.li.classList.add("gezogen");
    tree.classList.add("zieht");
    drag.li.querySelector(".zeile").setPointerCapture?.(drag.pointer);
  };

  const finish = () => {
    const t = target;
    const li = drag.li;
    const here = current(li);
    stop();
    if (!t) return;
    const ziel = place(t);
    if (ziel === here) return;
    form.action = `/seiten/${li.dataset.slug}/verschieben`;
    form.elements.ziel.value = ziel;
    form.requestSubmit();
  };

  tree.addEventListener("pointerdown", (e) => {
    if (e.button !== 0 || !e.isPrimary) return;
    const row = e.target.closest?.(".zeile");
    const li = row?.parentElement;
    if (!li?.hasAttribute("data-movable")) return;
    drag = { li, id: li.dataset.id, pointer: e.pointerId, x: e.clientX, y: e.clientY, active: false, timer: 0 };
    if (e.pointerType !== "mouse") drag.timer = setTimeout(begin, HOLD_MS);
  });

  tree.addEventListener("pointermove", (e) => {
    if (!drag || e.pointerId !== drag.pointer) return;
    if (!drag.active) {
      const far = Math.hypot(e.clientX - drag.x, e.clientY - drag.y) > MOVE_PX;
      if (e.pointerType === "mouse") {
        if (far) begin();
      } else if (far) {
        stop(); // the finger scrolls the page instead
        return;
      }
      if (!drag?.active) return;
    }
    show(locate(e.clientX, e.clientY));
  });

  tree.addEventListener("pointerup", (e) => {
    if (!drag || e.pointerId !== drag.pointer) return;
    if (drag.active) {
      e.preventDefault();
      finish();
    } else {
      stop();
    }
  });
  tree.addEventListener("pointercancel", stop);

  // Once a drag runs, a touch must not scroll the page, and the click that
  // ends a drag must not open the link.
  tree.addEventListener("touchmove", (e) => {
    if (drag?.active) e.preventDefault();
  }, { passive: false });
  tree.addEventListener("click", (e) => {
    if (drag?.active) e.preventDefault();
  }, true);
  tree.addEventListener("contextmenu", (e) => {
    if (drag?.active || drag?.timer) e.preventDefault();
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && drag) stop();
  });
}
