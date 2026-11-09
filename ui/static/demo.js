// Only in the static demo (allmend demo): filter forms open the exported
// page for their values (/velo?jahr=2025 → velo/~jahr=2025/, keys sorted, as
// in internal/demo), all other forms show that nothing is saved.
const safe = /^[A-Za-z0-9_.-]*$/;

function notice() {
  const main = document.querySelector("main");
  if (!main || main.querySelector(".demo-notice")) return;
  const p = document.createElement("p");
  p.className = "flash demo-notice";
  p.setAttribute("role", "status");
  p.textContent = "Das ist die Demo: Änderungen werden nicht gespeichert.";
  main.prepend(p);
  p.scrollIntoView({ block: "nearest" });
}

document.addEventListener("submit", (event) => {
  const form = event.target;
  event.preventDefault();
  if (form.method !== "get") {
    notice();
    return;
  }
  const pairs = [];
  for (const [k, v] of new FormData(form)) {
    if (typeof v !== "string" || !safe.test(k) || !safe.test(v)) {
      notice();
      return;
    }
    pairs.push(`${k}=${v}`);
  }
  pairs.sort((a, b) => {
    const ka = a.split("=")[0];
    const kb = b.split("=")[0];
    return ka < kb ? -1 : ka > kb ? 1 : 0;
  });
  const target = new URL(form.getAttribute("action"), location.href);
  if (pairs.length > 0) target.pathname += `~${pairs.join(",")}/`;
  location.href = target.href;
});
