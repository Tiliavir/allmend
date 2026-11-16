// Renders ```mermaid and ```markmap blocks on pages that contain them
// (Mermaid is loaded before this module by the page, Markmap is imported
// below). Nothing else on the page needs JS.
const dark = getComputedStyle(document.documentElement).colorScheme === "dark";

const mermaidBlocks = document.querySelectorAll("pre.mermaid");
if (mermaidBlocks.length > 0 && globalThis.mermaid) {
  globalThis.mermaid.initialize({ startOnLoad: false, securityLevel: "strict", theme: dark ? "dark" : "neutral" });
  await globalThis.mermaid.run({ nodes: mermaidBlocks });
}

// Markmap 1.x (bundled by tools/markmap) is imported only when the page has
// a ```markmap block. It reads the block itself: front matter options
// (theme, layout, foldLevel, …) and magic comments. Options it cannot use are
// listed under the map, as Markmap's own editor does.
const markmapBlocks = document.querySelectorAll("pre.markmap");
if (markmapBlocks.length > 0) {
  const { parse, Markmap, resolveViewOptions } = await import("/static/vendor/markmap-1.1.1.js");
  for (const pre of markmapBlocks) {
    const { root, frontmatter } = parse(pre.textContent);
    const own = frontmatter.markmap && typeof frontmatter.markmap === "object" ? frontmatter.markmap : {};
    const { options, warnings } = resolveViewOptions({ colorScheme: dark ? "dark" : "light" }, own);
    const box = document.createElement("div");
    box.className = "markmap-box";
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.classList.add("markmap");
    box.append(svg);
    if (warnings.length > 0) {
      const list = document.createElement("ul");
      list.className = "markmap-warnings";
      for (const w of warnings) {
        const li = document.createElement("li");
        li.textContent = `Markmap: ${w}`;
        list.append(li);
      }
      box.append(list);
    }
    pre.replaceWith(box);
    Markmap.create(svg, root, options);
  }
}
