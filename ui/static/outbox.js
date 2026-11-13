// The page "Nicht übertragen": lists the outbox entries the service worker
// keeps, lets the user open a rejected answer or discard an entry.
import { ask } from "./offline.js";

const list = document.getElementById("outbox");
const empty = document.getElementById("outbox-empty");
const status = document.getElementById("outbox-status");
const tpl = document.getElementById("outbox-item");

const timeFmt = new Intl.DateTimeFormat("de-CH", {
  timeZone: "Europe/Zurich",
  day: "numeric",
  month: "numeric",
  year: "numeric",
  hour: "2-digit",
  minute: "2-digit",
});

function stateText(e) {
  if (e.foreign) return "Mit einem anderen Konto erfasst – wird mit diesem Konto nicht übertragen.";
  if (e.state === "abgelehnt") return `Vom Server abgelehnt (${e.status}). Öffne die Antwort, korrigiere die Eingaben und sende das Formular erneut, oder verwirf den Eintrag.`;
  return "Wartet auf Übertragung.";
}

function render(s) {
  if (!s || s.type !== "outbox") return;
  list.replaceChildren();
  for (const e of s.entries) {
    const li = tpl.content.firstElementChild.cloneNode(true);
    li.classList.toggle("rejected", e.state === "abgelehnt");
    li.querySelector("[data-label]").textContent = e.label;
    li.querySelector("[data-time]").textContent = `erfasst ${timeFmt.format(new Date(e.created))}`;
    li.querySelector("[data-state]").textContent = stateText(e);
    const open = li.querySelector("[data-open]");
    if (e.state === "abgelehnt") {
      open.hidden = false;
      open.href = `/offline/eintrag/${e.id}`;
    }
    li.querySelector("[data-discard]").addEventListener("click", () => {
      ask({ type: "discard", id: e.id }).then(render);
    });
    list.append(li);
  }
  empty.hidden = s.entries.length > 0;
  const msg = { offline: "Allmend ist nicht erreichbar – die Einträge warten.", anmelden: "Bitte melde dich neu an; danach werden die Einträge übertragen." }[s.result];
  status.hidden = !msg;
  status.textContent = msg ?? "";
}

if (!navigator.serviceWorker) {
  status.hidden = false;
  status.textContent = "Dieser Browser unterstützt keinen Offline-Betrieb.";
} else {
  navigator.serviceWorker.addEventListener("message", (e) => render(e.data));
  ask({ type: "replay" }).then(render, (e) => {
    status.hidden = false;
    status.textContent = `Fehler: ${e.message}`;
  });
}
