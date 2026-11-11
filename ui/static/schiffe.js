// Live updates for a battleships game: the server sends the game version via
// Server-Sent Events; when it differs from the shown one, the page reloads.
// Without JS, the "Aktualisieren" button does the same.
const el = document.getElementById("schiffe");
if (el && "EventSource" in globalThis) {
  const shown = el.dataset.version;
  const es = new EventSource(el.dataset.events);
  es.addEventListener("update", (e) => {
    if (e.data !== shown) {
      es.close();
      location.reload();
    }
  });
}
// Salvo: keep the number of ticked cells at the allowed maximum.
const form = document.querySelector("table.target")?.closest("form");
const button = form?.querySelector("button.button");
if (form && button) {
  const max = Number(document.querySelector("[data-shots]")?.dataset.shots ?? 0);
  const update = () => {
    const n = form.querySelectorAll("input[name=c]:checked").length;
    button.textContent = `Salve abfeuern (${n}/${max})`;
    button.disabled = n !== max;
  };
  form.addEventListener("change", update);
  update();
}

// Setup: the board edits the setup form. Tap a ship to turn it around its
// middle; drag ships and mines to move them. Overlaps (and touching, if not
// allowed) turn red; the server still checks the fleet. Without JS the list
// in the form does the same.
const sea = document.querySelector("table.sea.setup");
const setupForm = sea?.closest("form");
if (sea && setupForm) {
  const n = Number(sea.dataset.n);
  const touching = sea.dataset.touching === "1";
  const cells = new Map(); // "x,y" -> td
  for (const td of sea.querySelectorAll("td[data-x]")) {
    cells.set(`${td.dataset.x},${td.dataset.y}`, td);
  }
  const parse = (v) => {
    const m = /^([A-La-l])(\d{1,2})$/.exec(v.trim());
    if (!m) return null;
    const x = m[1].toUpperCase().charCodeAt(0) - 65;
    const y = Number(m[2]) - 1;
    return x < n && y >= 0 && y < n ? { x, y } : null;
  };
  const coord = (x, y) => String.fromCharCode(65 + x) + (y + 1);

  // Objects in form order: ships (start + direction), then mines.
  const starts = [...setupForm.querySelectorAll("input[name=start]")];
  const dirs = [...setupForm.querySelectorAll("select[name=richtung]")];
  const objs = starts.map((input, i) => ({ size: Number(input.dataset.size), input, dir: dirs[i] }));
  for (const input of setupForm.querySelectorAll("input[name=mine]")) {
    objs.push({ size: 1, input, dir: null });
  }
  const pos = (o) => {
    const p = parse(o.input.value);
    return p && { ...p, h: o.dir ? o.dir.value === "h" : true };
  };
  const cellsOf = (p, size) =>
    Array.from({ length: size }, (_, i) => (p.h ? [p.x + i, p.y] : [p.x, p.y + i]));
  const clamp = (p, size) => ({
    ...p,
    x: Math.max(0, Math.min(p.x, n - (p.h ? size : 1))),
    y: Math.max(0, Math.min(p.y, n - (p.h ? 1 : size))),
  });
  const write = (o, p) => {
    o.input.value = coord(p.x, p.y);
    if (o.dir) o.dir.value = p.h ? "h" : "v";
  };

  let drag = null; // { o, grab, start, moved, target }
  const render = () => {
    const owner = new Map(); // "x,y" -> [object index]
    objs.forEach((o, i) => {
      if (drag?.moved && drag.o === o) return;
      const p = pos(o);
      if (!p) return;
      for (const [x, y] of cellsOf(p, o.size)) {
        const k = `${x},${y}`;
        owner.set(k, [...(owner.get(k) ?? []), i]);
      }
    });
    const bad = new Set();
    for (const [k, list] of owner) {
      if (list.length > 1) bad.add(k);
      if (touching) continue;
      const [x, y] = k.split(",").map(Number);
      for (let dx = -1; dx <= 1; dx++) {
        for (let dy = -1; dy <= 1; dy++) {
          const other = owner.get(`${x + dx},${y + dy}`);
          if (other?.some((j) => !list.includes(j))) bad.add(k);
        }
      }
    }
    const drop = new Set();
    const lifted = new Set();
    if (drag?.moved) {
      for (const [x, y] of cellsOf(drag.target, drag.o.size)) drop.add(`${x},${y}`);
      const p = pos(drag.o);
      if (p) for (const [x, y] of cellsOf(p, drag.o.size)) lifted.add(`${x},${y}`);
    }
    for (const [k, td] of cells) {
      const list = owner.get(k);
      const isMine = list?.length === 1 && objs[list[0]].size === 1;
      const lift = lifted.has(k) && !list;
      td.className = list || lift ? (isMine || (lift && drag.o.size === 1) ? "mine" : "ship") : "empty";
      td.classList.toggle("conflict", bad.has(k));
      td.classList.toggle("drop", drop.has(k));
      td.classList.toggle("lifted", lift);
    }
  };

  const objectAt = (td) => {
    const x = Number(td.dataset.x);
    const y = Number(td.dataset.y);
    for (let i = objs.length - 1; i >= 0; i--) {
      const p = pos(objs[i]);
      if (p && cellsOf(p, objs[i].size).some(([cx, cy]) => cx === x && cy === y)) {
        return { o: objs[i], grab: p.h ? x - p.x : y - p.y };
      }
    }
    return null;
  };
  const cellAt = (e) => document.elementFromPoint(e.clientX, e.clientY)?.closest("td[data-x]");

  sea.addEventListener("pointerdown", (e) => {
    const td = e.target.closest("td[data-x]");
    const hit = td && objectAt(td);
    if (!hit || e.button > 0) return;
    e.preventDefault();
    sea.setPointerCapture(e.pointerId);
    drag = { ...hit, start: td, moved: false, target: pos(hit.o) };
  });
  sea.addEventListener("pointermove", (e) => {
    if (!drag) return;
    const td = cellAt(e);
    if (!td || (!drag.moved && td === drag.start)) return;
    const p = pos(drag.o);
    const x = Number(td.dataset.x) - (p.h ? drag.grab : 0);
    const y = Number(td.dataset.y) - (p.h ? 0 : drag.grab);
    drag.moved = true;
    drag.target = clamp({ x, y, h: p.h }, drag.o.size);
    sea.classList.add("dragging");
    render();
  });
  const end = (commit) => {
    if (!drag) return;
    const { o, moved, target } = drag;
    drag = null;
    sea.classList.remove("dragging");
    if (commit && moved) {
      write(o, target);
    } else if (commit && o.size > 1) {
      // Turn around the middle: the middle cell keeps its place.
      const p = pos(o);
      const k = Math.floor((o.size - 1) / 2);
      const turned = p.h ? { x: p.x + k, y: p.y - k, h: false } : { x: p.x - k, y: p.y + k, h: true };
      write(o, clamp(turned, o.size));
    }
    render();
  };
  sea.addEventListener("pointerup", () => end(true));
  sea.addEventListener("pointercancel", () => end(false));
  sea.addEventListener("lostpointercapture", () => end(false));
  setupForm.addEventListener("input", render);
  setupForm.addEventListener("change", render);

  sea.classList.add("live");
  document.getElementById("setup-hint")?.removeAttribute("hidden");
  setupForm.querySelector("details.fleet-list")?.removeAttribute("open");
  render();
}
