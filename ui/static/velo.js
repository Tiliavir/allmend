// Cumulative distance chart on the Velo overview. Data comes from the
// <script type="application/json" id="velo-data"> block rendered by the server;
// the same numbers are in the year table, so the page works without JS.
const el = document.getElementById("velo-data");
const canvas = document.getElementById("velo-chart");
if (el && canvas && globalThis.Chart) {
  const data = JSON.parse(el.textContent);
  const css = getComputedStyle(document.body);
  const accent = css.getPropertyValue("--accent").trim();
  const muted = css.getPropertyValue("--muted").trim();
  const border = css.getPropertyValue("--border").trim();
  const labels = Array.from({ length: Math.max(data.this.length, data.prev.length) }, (_, i) => {
    const d = new Date(Date.UTC(data.year, 0, 1 + i));
    return d.toLocaleDateString("de-CH", { day: "numeric", month: "short", timeZone: "UTC" });
  });
  new globalThis.Chart(canvas, {
    type: "line",
    data: {
      labels,
      datasets: [
        { label: String(data.year), data: data.this, borderColor: accent, backgroundColor: accent, borderWidth: 3, pointRadius: 0, tension: 0.15 },
        { label: String(data.year - 1), data: data.prev, borderColor: muted, backgroundColor: muted, borderWidth: 2, borderDash: [6, 6], pointRadius: 0, tension: 0.15 },
      ],
    },
    options: {
      animation: false,
      maintainAspectRatio: false,
      interaction: { mode: "index", intersect: false },
      scales: {
        x: { ticks: { color: muted, maxTicksLimit: 12, autoSkip: true }, grid: { display: false } },
        y: { ticks: { color: muted, callback: (v) => `${v.toLocaleString("de-CH")} km` }, grid: { color: border }, beginAtZero: true },
      },
      plugins: {
        legend: { labels: { color: muted, boxHeight: 2 } },
        tooltip: { callbacks: { label: (c) => ` ${c.dataset.label}: ${Math.round(c.parsed.y).toLocaleString("de-CH")} km` } },
      },
    },
  });
}
