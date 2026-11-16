// Fills ui/static/vendor from the pinned packages in package.json (ADR
// vendor-build): ready-made browser files are copied, Markmap 1.x (ES
// modules only) is bundled with esbuild. Scripts are stored gzipped only
// (the server sends name.gz for name). LICENSES.txt holds the license of
// every package that ships code to the browser. Run via `make vendor`.
import { build } from "esbuild";
import { mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { gzipSync } from "node:zlib";

const out = new URL("../../ui/static/vendor/", import.meta.url).pathname;

// file in ui/static/vendor ← file in node_modules (stable names: code refers
// to them; the server revalidates by ETag, so a new version is picked up)
const copies = {
  "mermaid.min.js": "mermaid/dist/mermaid.min.js",
  "chart.umd.min.js": "chart.js/dist/chart.umd.min.js",
  "leaflet.js": "leaflet/dist/leaflet.js",
  "leaflet.css": "leaflet/dist/leaflet.css",
  "leaflet-heat.js": "leaflet.heat/dist/leaflet-heat.js",
};

rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });
const write = (name, data) => {
  if (name.endsWith(".js")) writeFileSync(join(out, `${name}.gz`), gzipSync(data, { level: 9 }));
  else writeFileSync(join(out, name), data);
};

const packages = new Set();
for (const [name, from] of Object.entries(copies)) {
  write(name, readFileSync(join("node_modules", from)));
  packages.add(from.split("/")[0]);
}

// Markmap: entry markmap.js, chunks carry a content hash
const tmp = join(tmpdir(), `allmend-markmap-${process.pid}`);
const result = await build({
  entryPoints: { markmap: "entry-markmap.js" },
  bundle: true,
  minify: true,
  format: "esm",
  splitting: true,
  target: "es2022",
  chunkNames: "markmap-[name]-[hash]",
  legalComments: "none",
  metafile: true,
  outdir: tmp,
  logLevel: "warning",
});
for (const f of readdirSync(tmp)) write(f, readFileSync(join(tmp, f)));
rmSync(tmp, { recursive: true });
for (const input of Object.keys(result.metafile.inputs)) {
  const m = input.match(/node_modules\/((?:@[^/]+\/)?[^/]+)\//);
  if (m) packages.add(m[1]);
}

writeFileSync(join(out, "LICENSES.txt"), [...packages].sort().map(license).join(""));
console.log(`${basename(out)}: ${readdirSync(out).length} files, ${packages.size} packages`);

function license(name) {
  const dir = join("node_modules", name);
  const pkg = JSON.parse(readFileSync(join(dir, "package.json"), "utf8"));
  const kind = pkg.license ?? pkg.licenses?.map((l) => l.type).join(", ") ?? "license below";
  const file = readdirSync(dir).find((f) => /^(licen[cs]e|copying)/i.test(f));
  let text = file && readFileSync(join(dir, file), "utf8").trim();
  if (!text) {
    // no license file: the License section of the readme
    const readme = readdirSync(dir).find((f) => /^readme/i.test(f));
    text = readme && readFileSync(join(dir, readme), "utf8").match(/\nLicen[cs]e\n[=-]+\n([\s\S]*?)(\n\S[^\n]*\n[=-]+\n|$)/)?.[1].trim();
  }
  if (!text) throw new Error(`${name}: no license found`);
  return `${name}@${pkg.version} (${kind})\n\n${text}\n\n${"-".repeat(72)}\n\n`;
}
