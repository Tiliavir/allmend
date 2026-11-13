// Category picker: search field and a live summary. Without JS the picker
// still works (open it, pick a card); only the search is missing.
for (const picker of document.querySelectorAll("details.ag-picker")) {
  const search = picker.querySelector(".ag-search");
  const input = search.querySelector("input");
  const none = picker.querySelector(".ag-none");
  search.hidden = false;
  input.addEventListener("input", () => {
    const q = input.value.trim().toLowerCase();
    let shown = 0;
    for (const group of picker.querySelectorAll(".ag-group")) {
      let n = 0;
      for (const opt of group.querySelectorAll(".ag-opt")) {
        const hit = q === "" || opt.dataset.search.toLowerCase().includes(q);
        opt.hidden = !hit;
        if (hit) n++;
      }
      group.hidden = n === 0;
      shown += n;
    }
    none.hidden = shown > 0;
  });
  picker.addEventListener("change", (ev) => {
    const opt = ev.target.closest(".ag-opt");
    if (!opt) return;
    const group = opt.closest(".ag-group");
    const card = document.createElement("span");
    card.className = `ag-card ag-cur ${group.querySelector(".ag-opts").className.replace("ag-opts", "").trim()}`;
    card.append(opt.querySelector(".ag-ico").cloneNode(true), opt.querySelector("span").cloneNode(true));
    const summary = picker.querySelector("summary");
    const text = document.createElement("span");
    text.className = "ag-cur-txt";
    const main = document.createElement("b");
    main.textContent = group.querySelector("h2").textContent;
    const hint = document.createElement("span");
    hint.className = "muted small";
    hint.textContent = "Kategorie ändern";
    text.append(main, hint);
    summary.replaceChildren(card, text, summary.querySelector(".ag-chev"));
    picker.open = false;
    input.value = "";
    input.dispatchEvent(new Event("input"));
  });
}
