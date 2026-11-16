#!/bin/sh
# Bundles Markmap 1.x (@markmap/parse + @markmap/view, pinned in
# package.json) into ui/static/vendor as gzipped ES modules with the version
# in every file name, plus the licenses of everything bundled. Run after
# changing the version: make vendor-markmap (and update the import path in
# ui/static/seiten.js)
set -eu
cd "$(dirname "$0")"
npm ci --silent
version=$(node -p 'require("./package.json").devDependencies["@markmap/view"]')
out=../../ui/static/vendor
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
npx esbuild entry.js --bundle --minify --format=esm --splitting --target=es2022 \
  --entry-names="markmap-$version" --chunk-names="markmap-$version-[name]-[hash]" \
  --legal-comments=none --metafile="$tmp/meta.json" --outdir="$tmp/out" --log-level=warning
rm -f "$out"/markmap-*.js.gz "$out"/markmap-*.LICENSE.txt
for f in "$tmp"/out/*.js; do gzip -9 -n -c "$f" > "$out/$(basename "$f").gz"; done
node licenses.mjs "$tmp/meta.json" > "$out/markmap-$version.LICENSE.txt"
ls -l "$out"/markmap-*
