// Live updates for a battleships game: the server sends the game version via
// Server-Sent Events; when it differs from the shown one, the page reloads.
// Without JS, the "Aktualisieren" button does the same.
const el = document.getElementById("schiffe");
if (el && "EventSource" in globalThis) {
  const shown = el.dataset.version;
  const es = new EventSource(el.dataset.events);
  es.addEventListener("update", (e) => {
    if (e.data !== shown) {
      es.close();
      location.reload();
    }
  });
}
// Salvo: keep the number of ticked cells at the allowed maximum.
const form = document.querySelector("table.target")?.closest("form");
const button = form?.querySelector("button.button");
if (form && button) {
  const max = Number(document.querySelector("[data-shots]")?.dataset.shots ?? 0);
  const update = () => {
    const n = form.querySelectorAll("input[name=c]:checked").length;
    button.textContent = `Salve abfeuern (${n}/${max})`;
    button.disabled = n !== max;
  };
  form.addEventListener("change", update);
  update();
}
