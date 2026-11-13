// Registers the service worker (ADR offline), shows the outbox in the header
// and starts the transfer on every page load and when the device comes back
// online. Without service worker support nothing happens.
const sw = navigator.serviceWorker;
const link = document.querySelector("[data-outbox]");

/** ask sends msg to the active worker and resolves with its answer. */
export async function ask(msg) {
  if (!sw) return null;
  const reg = await sw.ready;
  return new Promise((resolve, reject) => {
    const ch = new MessageChannel();
    ch.port1.onmessage = (e) => (e.data?.type === "error" ? reject(new Error(e.data.message)) : resolve(e.data));
    reg.active.postMessage(msg, [ch.port2]);
  });
}

let last = { waiting: 0, rejected: 0, result: "ok" };

function show(s) {
  if (!s || s.type !== "outbox") return;
  last = { ...s, result: s.result ?? last.result };
  if (!link) return;
  const n = s.waiting + s.rejected;
  link.hidden = n === 0;
  link.classList.toggle("rejected", s.rejected > 0);
  let text = `${n} nicht übertragen`;
  if (s.rejected > 0) text += ` (${s.rejected} abgelehnt)`;
  if (last.result === "anmelden" && s.waiting > 0) text += " – bitte neu anmelden";
  link.textContent = text;
}

function transfer() {
  ask({ type: "replay" }).then(show, () => {});
}

if (sw) {
  sw.register("/sw.js").catch(() => {});
  sw.addEventListener("message", (e) => show(e.data));
  addEventListener("online", transfer);
  transfer();

  // Logout clears the outbox (Clear-Site-Data): warn first if it is not empty.
  const logout = document.querySelector('form[action="/abmelden"]');
  logout?.addEventListener("submit", (e) => {
    const n = last.waiting + last.rejected;
    if (n === 0 || logout.dataset.confirmed === "1") return;
    e.preventDefault();
    if (logout.querySelector(".logout-warning")) return;
    const p = document.createElement("p");
    p.className = "logout-warning flash error";
    p.setAttribute("role", "alert");
    p.textContent = `${n} offline erfasste Einträge sind noch nicht übertragen und gehen beim Abmelden verloren. `;
    const btn = document.createElement("button");
    btn.type = "submit";
    btn.textContent = "Trotzdem abmelden";
    btn.addEventListener("click", () => {
      logout.dataset.confirmed = "1";
    });
    p.append(btn);
    logout.append(p);
  });
}
