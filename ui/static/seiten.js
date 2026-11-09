// Renders ```mermaid blocks on pages that contain them (Mermaid is loaded
// before this module by the page). Nothing else on the page needs JS.
const blocks = document.querySelectorAll("pre.mermaid");
if (blocks.length > 0 && globalThis.mermaid) {
  const dark = matchMedia("(prefers-color-scheme: dark)").matches;
  globalThis.mermaid.initialize({ startOnLoad: false, securityLevel: "strict", theme: dark ? "dark" : "neutral" });
  await globalThis.mermaid.run({ nodes: blocks });
}
