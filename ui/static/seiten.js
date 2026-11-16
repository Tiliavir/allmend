// Renders ```mermaid and ```markmap blocks on pages that contain them (the
// libraries are loaded before this module by the page). Nothing else on the
// page needs JS.
const dark = getComputedStyle(document.documentElement).colorScheme === "dark";

const mermaidBlocks = document.querySelectorAll("pre.mermaid");
if (mermaidBlocks.length > 0 && globalThis.mermaid) {
  globalThis.mermaid.initialize({ startOnLoad: false, securityLevel: "strict", theme: dark ? "dark" : "neutral" });
  await globalThis.mermaid.run({ nodes: mermaidBlocks });
}

// The server turned each block into a node tree (data-markmap, safe HTML)
// and checked its options (data-markmap-options, from the block's front
// matter). Defaults: groups keep their colour, the first three levels are
// open; a click on a circle opens or closes the branch.
const markmapBlocks = document.querySelectorAll("pre.markmap[data-markmap]");
if (markmapBlocks.length > 0 && globalThis.markmap) {
  const { Markmap, deriveOptions } = globalThis.markmap;
  const defaults = { colorFreezeLevel: 2, initialExpandLevel: 3, maxWidth: 300 };
  for (const pre of markmapBlocks) {
    const data = JSON.parse(pre.dataset.markmap);
    const options = deriveOptions({ ...defaults, ...JSON.parse(pre.dataset.markmapOptions ?? "{}") });
    const box = document.createElement("div");
    box.className = dark ? "markmap-box markmap-dark" : "markmap-box";
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.classList.add("markmap");
    box.append(svg);
    pre.replaceWith(box);
    Markmap.create(svg, options, data);
  }
}
