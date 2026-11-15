// Heatmap of everything (ADR-20261115-orte). The server sends cells, never
// raw points: the page carries the cells for the first view; when the map
// moves, finer cells for the view come from /orte/karte/zellen (not in the
// static demo, "live": false). Needs Leaflet and Leaflet.heat.
const el = document.getElementById("karte");
const src = document.getElementById("karte-daten");
const L = globalThis.L;
if (el && src && L && L.heatLayer) {
  const data = JSON.parse(src.textContent);
  const map = L.map(el, { scrollWheelZoom: false });
  L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
    maxZoom: 19,
    referrerPolicy: "strict-origin-when-cross-origin", // see orte.js
    attribution: "© OpenStreetMap-Mitwirkende",
  }).addTo(map);
  // Counts span orders of magnitude (home vs. a holiday): log scale.
  const points = (cells) => {
    let top = 1;
    for (const c of cells) top = Math.max(top, c[2]);
    return cells.map((c) => [c[0], c[1], Math.log1p(c[2]) / Math.log1p(top)]);
  };
  const heat = L.heatLayer(points(data.cells), { radius: 18, blur: 15, max: 1, minOpacity: 0.35 }).addTo(map);
  map.fitBounds(data.bounds, { padding: [24, 24], maxZoom: 13 });
  let pending;
  const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));
  const reload = async () => {
    const b = map.getBounds();
    const q = new URLSearchParams({
      z: String(map.getZoom()),
      s: clamp(b.getSouth(), -90, 90).toFixed(5),
      w: clamp(b.getWest(), -180, 180).toFixed(5),
      n: clamp(b.getNorth(), -90, 90).toFixed(5),
      e: clamp(b.getEast(), -180, 180).toFixed(5),
    });
    try {
      const r = await fetch("/orte/karte/zellen?" + q, { credentials: "same-origin" });
      if (r.ok) heat.setLatLngs(points(await r.json()));
    } catch {
      // Offline: keep what is drawn.
    }
  };
  if (data.live) {
    map.on("moveend", () => {
      clearTimeout(pending);
      pending = setTimeout(reload, 250);
    });
    reload();
  }
}
