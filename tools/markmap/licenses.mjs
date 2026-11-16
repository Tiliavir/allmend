// Prints the license of every npm package that went into the bundle
// (from esbuild's metafile), for ui/static/vendor/markmap-*.LICENSE.txt.
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";

const meta = JSON.parse(readFileSync(process.argv[2], "utf8"));
const pkgs = new Set();
for (const input of Object.keys(meta.inputs)) {
  const m = input.match(/node_modules\/((?:@[^/]+\/)?[^/]+)\//);
  if (m) pkgs.add(m[1]);
}
for (const name of [...pkgs].sort()) {
  const dir = join("node_modules", name);
  const pkg = JSON.parse(readFileSync(join(dir, "package.json"), "utf8"));
  const license = pkg.license ?? pkg.licenses?.map((l) => l.type).join(", ");
  const file = readdirSync(dir).find((f) => /^(licen[cs]e|copying)/i.test(f));
  let text = file && readFileSync(join(dir, file), "utf8").trim();
  if (!text) {
    // no license file: the License section of the readme
    const readme = readdirSync(dir).find((f) => /^readme/i.test(f));
    text = readme && readFileSync(join(dir, readme), "utf8").match(/\nLicen[cs]e\n[=-]+\n([\s\S]*?)(\n\S[^\n]*\n[=-]+\n|$)/)?.[1].trim();
  }
  if (!license || !text) throw new Error(`${name}: no license found`);
  console.log(`${name}@${pkg.version} (${license})\n\n${text}\n\n${"-".repeat(72)}\n`);
}
