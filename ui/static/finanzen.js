// Charts on the finance pages. Each <canvas data-chart="id"> reads its data
// from <script type="application/json" id="id">: {type, labels, unit, stacked,
// baseline, series: [{label, data, fill}]}. The numbers are also in the tables,
// so the pages work without JS. Colours: validated categorical palette
// (--series-1…8, own steps for dark mode), assigned in fixed order.
const css = getComputedStyle(document.body);
const v = (name) => css.getPropertyValue(name).trim();
const muted = v("--muted");
const border = v("--border");
const palette = Array.from({ length: 8 }, (_, i) => v(`--series-${i + 1}`));
const surface = v("--surface");

// Same number style as the server: Swiss for CHF, German otherwise.
const locale = (unit) => (unit === "CHF" ? "de-CH" : "de-DE");
const fmt = (unit, digits) => (n) =>
  n == null ? "–" : `${n.toLocaleString(locale(unit), { maximumFractionDigits: digits })}${unit ? ` ${unit}` : ""}`;
const isoDate = /^\d{4}-\d{2}-\d{2}$/;
const day = (s) => (isoDate.test(s) ? `${s.slice(8, 10)}.${s.slice(5, 7)}.${s.slice(0, 4)}` : s);
const month = (s) =>
  isoDate.test(s) ? new Date(`${s}T00:00:00Z`).toLocaleDateString("de-DE", { month: "short", year: "numeric", timeZone: "UTC" }) : s;

for (const canvas of document.querySelectorAll("canvas[data-chart]")) {
  const el = document.getElementById(canvas.dataset.chart);
  if (!el || !globalThis.Chart) continue;
  const d = JSON.parse(el.textContent);
  const bar = d.type === "bar";
  const single = d.series.length === 1;
  const color = (i) => (single && !bar ? v("--accent") : palette[i % palette.length]);
  const datasets = d.series.map((s, i) => ({
    label: s.label,
    data: s.data,
    borderColor: color(i),
    backgroundColor: bar ? color(i) : s.fill ? `${color(i)}26` : color(i), // hex + 15 % alpha
    fill: s.fill ? "origin" : false,
    borderWidth: bar ? { top: 0, right: 0, bottom: 0, left: 0 } : 2,
    borderRadius: bar ? 4 : 0,
    pointRadius: 0,
    pointHoverRadius: 4,
    spanGaps: true,
    tension: 0.1,
    stack: d.stacked ? "s" : undefined,
  }));
  if (bar && d.stacked) {
    // 2px surface gap between stacked segments.
    for (const ds of datasets) {
      ds.borderColor = surface;
      ds.borderWidth = { top: 2, right: 0, bottom: 0, left: 0 };
      ds.borderSkipped = "bottom";
    }
  }
  const y = fmt(d.unit, d.unit ? 0 : 1);
  new globalThis.Chart(canvas, {
    type: bar ? "bar" : "line",
    data: { labels: d.labels, datasets },
    options: {
      animation: false,
      maintainAspectRatio: false,
      interaction: { mode: "index", intersect: false },
      scales: {
        x: {
          stacked: !!d.stacked,
          ticks: { color: muted, maxTicksLimit: 10, autoSkip: true, maxRotation: 0, callback: (_, i) => month(d.labels[i]) },
          grid: { display: false },
        },
        y: {
          stacked: !!d.stacked && bar,
          ticks: { color: muted, callback: y },
          grid: { color: (c) => (d.baseline != null && c.tick.value === d.baseline ? muted : border) },
          beginAtZero: bar,
        },
      },
      plugins: {
        legend: { display: !single, labels: { color: muted, boxWidth: 12, boxHeight: 12 } },
        tooltip: { callbacks: { title: (items) => day(d.labels[items[0].dataIndex]), label: (c) => ` ${c.dataset.label}: ${y(c.parsed.y)}` } },
      },
    },
  });
}
