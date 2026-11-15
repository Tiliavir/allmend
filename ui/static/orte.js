// Draws the map of a trip or a day: places as circles, the track as a line,
// the owner's location history (trip) dashed, stays (day) as rings. Data
// comes from <script type="application/json" id="karte-daten">.
const el = document.getElementById("karte");
const src = document.getElementById("karte-daten");
const L = globalThis.L;
if (el && src && L) {
  const data = JSON.parse(src.textContent);
  const css = getComputedStyle(document.body);
  const accent = css.getPropertyValue("--accent").trim();
  const muted = css.getPropertyValue("--muted").trim();
  const map = L.map(el, { scrollWheelZoom: false });
  L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
    maxZoom: 19,
    // The site sends "Referrer-Policy: same-origin", so tile requests would carry
    // no Referer at all — OSM's tile usage policy rejects those with 403.
    // Send just the origin for the tiles; all other cross-origin requests stay
    // referrer-free.
    referrerPolicy: "strict-origin-when-cross-origin",
    attribution: "© OpenStreetMap-Mitwirkende",
  }).addTo(map);
  const bounds = [];
  const label = (text) => {
    const s = document.createElement("span");
    s.textContent = text;
    return s;
  };
  if (data.verlauf && data.verlauf.length > 0) {
    L.polyline(data.verlauf, { color: muted, weight: 2, opacity: 0.9, dashArray: "4 6" }).addTo(map);
    bounds.push(...data.verlauf);
  }
  if (data.path.length > 0) {
    L.polyline(data.path, { color: accent, weight: 3, opacity: 0.8 }).addTo(map);
    bounds.push(...data.path);
  }
  for (const p of data.places) {
    L.circleMarker([p.lat, p.lon], { radius: 7, color: accent, fillOpacity: 0.8 }).bindPopup(label(p.name)).addTo(map);
    bounds.push([p.lat, p.lon]);
  }
  for (const p of data.stays || []) {
    L.circleMarker([p.lat, p.lon], { radius: 8, color: accent, weight: 3, fillColor: "#fff", fillOpacity: 1 }).bindPopup(label(p.name)).addTo(map);
    bounds.push([p.lat, p.lon]);
  }
  if (bounds.length > 0) {
    map.fitBounds(bounds, { padding: [24, 24], maxZoom: 15 });
  } else {
    map.setView([47.56, 7.6], 9);
  }
}
