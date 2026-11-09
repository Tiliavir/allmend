// Draws the map of a trip or a day: places as circles, the GPS track as a
// line. Data comes from <script type="application/json" id="karte-daten">.
const el = document.getElementById("karte");
const src = document.getElementById("karte-daten");
const L = globalThis.L;
if (el && src && L) {
  const data = JSON.parse(src.textContent);
  const accent = getComputedStyle(document.body).getPropertyValue("--accent").trim();
  const map = L.map(el, { scrollWheelZoom: false });
  L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
    maxZoom: 19,
    attribution: "© OpenStreetMap-Mitwirkende",
  }).addTo(map);
  const bounds = [];
  if (data.path.length > 0) {
    L.polyline(data.path, { color: accent, weight: 3, opacity: 0.8 }).addTo(map);
    bounds.push(...data.path);
  }
  for (const p of data.places) {
    const label = document.createElement("span");
    label.textContent = p.name;
    L.circleMarker([p.lat, p.lon], { radius: 7, color: accent, fillOpacity: 0.8 }).bindPopup(label).addTo(map);
    bounds.push([p.lat, p.lon]);
  }
  if (bounds.length > 0) {
    map.fitBounds(bounds, { padding: [24, 24], maxZoom: 15 });
  } else {
    map.setView([47.56, 7.6], 9);
  }
}
