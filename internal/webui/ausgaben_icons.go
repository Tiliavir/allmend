package webui

import (
	"html/template"

	"github.com/tiliavir/allmend/internal/ausgaben"
)

// Line icons of the Ausgaben categories (24×24, stroke), drawn for Allmend.
// Trusted constants, never user input.
const (
	agReceipt = "M6 3h12v18l-2-1.5-2 1.5-2-1.5-2 1.5-2-1.5L6 21z"
	agOther   = agReceipt + "M9 11h.01M12 11h.01M15 11h.01"
	agHouse   = "M3 11l9-7 9 7M5 10v10h14V10"
	agCar     = "M3 16v-3l2-6h14l2 6v3zM5 18a2 2 0 1 0 4 0a2 2 0 1 0-4 0M15 18a2 2 0 1 0 4 0a2 2 0 1 0-4 0"
)

var agIcons = map[string]string{
	"groceries":  "M3 4h2l2.5 11h11l2-8H6.5M8 19a1 1 0 1 0 2 0a1 1 0 1 0-2 0M16 19a1 1 0 1 0 2 0a1 1 0 1 0-2 0",
	"dining_out": "M3 12a9 9 0 1 0 18 0a9 9 0 1 0-18 0M9 7v10M8 7v3a1 1 0 0 0 2 0V7M15 17V7c-1.2.6-2 2-2 4h2",
	"liquor":     "M8 3h8l-1 6a3 3 0 0 1-6 0zM12 12v8M8 21h8",
	"food_other": agOther,

	"rent":               agHouse + "M10 20v-6h4v6",
	"mortgage":           agHouse + "M14 13l-4 4M10 13h.01M14 17h.01",
	"furniture":          "M5 11V8a3 3 0 0 1 3-3h8a3 3 0 0 1 3 3v3M3 11a2 2 0 0 1 4 0v3h10v-3a2 2 0 0 1 4 0v6H3zM5 17v3M19 17v3",
	"electronics":        "M13 2L4 14h7l-1 8 9-12h-7z",
	"household_supplies": "M9 9h6v12H9zM10 9V6h3l2 1M12 6V3",
	"maintenance":        "M14.7 6.3a4 4 0 0 0-5.4 5.2L3 17.8V21h3.2l6.3-6.3a4 4 0 0 0 5.2-5.4l-2.5 2.5-2.3-.6-.6-2.3z",
	"pets":               "M12 13c-3 0-5 3-5 5a2 2 0 0 0 2 2c1 0 2-.5 3-.5s2 .5 3 .5a2 2 0 0 0 2-2c0-2-2-5-5-5zM5 10a1.5 1.5 0 1 0 3 0a1.5 1.5 0 1 0-3 0M8.5 6a1.5 1.5 0 1 0 3 0a1.5 1.5 0 1 0-3 0M12.5 6a1.5 1.5 0 1 0 3 0a1.5 1.5 0 1 0-3 0M16 10a1.5 1.5 0 1 0 3 0a1.5 1.5 0 1 0-3 0",
	"services":           "M4 17h16M12 8a7 7 0 0 0-7 7v2h14v-2a7 7 0 0 0-7-7zM12 5v3M10 5h4",
	"home_other":         agOther,

	"electricity":       "M9 18h6M10 21h4M12 3a6 6 0 0 0-4 10.5c.7.7 1 1.5 1 2.5h6c0-1 .3-1.8 1-2.5A6 6 0 0 0 12 3z",
	"heat_gas":          "M12 3c1 4 6 6 6 11a6 6 0 0 1-12 0c0-3 2-4 2-7 2 1 3 3 3 4 1-2 1-5 1-8z",
	"water":             "M12 3c3 4.5 6 8 6 11a6 6 0 0 1-12 0c0-3 3-6.5 6-11z",
	"trash":             "M4 7h16M10 11v6M14 11v6M5 7l1 14h12l1-14M9 7V4h6v3",
	"cleaning":          "M14 3l-3 9M6 12h9l2 9H4zM8 16v5M12 16v5",
	"tv_phone_internet": "M2 9a15 15 0 0 1 20 0M5 12.5a10 10 0 0 1 14 0M8.5 16a5 5 0 0 1 7 0M12 19.5h.01",
	"utilities_other":   agOther,

	"car":                  agCar,
	"gas_fuel":             "M4 21V5a2 2 0 0 1 2-2h6a2 2 0 0 1 2 2v16M3 21h12M4 10h10M14 8l3 2v7a1.5 1.5 0 0 0 3 0V9l-3-3",
	"parking":              "M4 4h16v16H4zM10 16V8h3a2.5 2.5 0 0 1 0 5h-3",
	"bus_train":            "M6 3h12a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2zM4 11h16M8 14h.01M16 14h.01M7 17l-2 4M17 17l2 4",
	"bicycle":              "M2.5 17a3.5 3.5 0 1 0 7 0a3.5 3.5 0 1 0-7 0M14.5 17a3.5 3.5 0 1 0 7 0a3.5 3.5 0 1 0-7 0M6 17l3.5-8h5L18 17M9.5 9l2.5 8M8 6h3",
	"taxi":                 agCar + "M10 7V4h4v3",
	"plane":                "M21 15l-8-4V5a1.5 1.5 0 0 0-3 0v6l-8 4v2l8-2v3l-2 1.5V21l3.5-1 3.5 1v-1.5L13 18v-3l8 2z",
	"hotel":                "M3 18V7M3 13h18v5M21 13a3 3 0 0 0-3-3h-7v3M5 10.5a1.5 1.5 0 1 0 3 0a1.5 1.5 0 1 0-3 0",
	"transportation_other": agOther,

	"movies":              "M3 7h18v3a2 2 0 0 0 0 4v3H3v-3a2 2 0 0 0 0-4zM14 7v10",
	"music":               "M9 18V5l11-2v13M3 18a3 3 0 1 0 6 0a3 3 0 1 0-6 0M14 16a3 3 0 1 0 6 0a3 3 0 1 0-6 0",
	"games":               "M6 9h12a4 4 0 0 1 3.5 6l-.8 1.5a2 2 0 0 1-3.2.4L15 15H9l-2.5 1.9a2 2 0 0 1-3.2-.4L2.5 15A4 4 0 0 1 6 9zM7 11v3M5.5 12.5h3M16 12h.01M18 13.5h.01",
	"sports":              "M3 12a9 9 0 1 0 18 0a9 9 0 1 0-18 0M12 7l4 3-1.5 4.5h-5L8 10z",
	"entertainment_other": agOther,

	"clothing":   "M12 8a2 2 0 1 1 2-2M12 8v1L3 16h18l-9-7",
	"gifts":      "M3 8h18v4H3zM5 12v9h14v-9M12 8v13M12 8C10 4 7 4 7 6s3 2 5 2M12 8c2-4 5-4 5-2s-3 2-5 2",
	"medical":    "M10 4h4v6h6v4h-6v6h-4v-6H4v-4h6z",
	"insurance":  "M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z",
	"childcare":  "M4 13h7v7H4zM13 13h7v7h-7zM8.5 5h7v8h-7z",
	"education":  "M2 9l10-5 10 5-10 5zM6 11v5c3 2 9 2 12 0v-5M22 9v6",
	"taxes":      "M6 3h12v18H6zM9 6h6v3H9zM9 13h.01M12 13h.01M15 13h.01M9 17h.01M12 17h.01M15 17h.01",
	"life_other": agOther,

	"general": agReceipt + "M9 8h6M9 12h6M9 16h4",
}

// agIcon renders the icon of category key (decorative: the name is always
// shown next to it).
func agIcon(key string) template.HTML {
	d, ok := agIcons[key]
	if !ok {
		d = agReceipt
	}
	return template.HTML(`<svg class="ag-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="` + d + `"/></svg>`) //nolint:gosec // constant markup
}

// agCat is a category as templates show it.
type agCat struct {
	Key, Name, Main, MainName string
	Short                     string // the subcategory's own name ("Sonstiges")
}

func agCatOf(key string) agCat {
	s, ok := ausgaben.Category(key)
	if !ok {
		return agCat{Key: key, Name: key}
	}
	m, _ := ausgaben.MainByKey(s.Main)
	name := s.Name
	if name == "Sonstiges" {
		name = m.Name + " – Sonstiges"
	}
	return agCat{Key: key, Name: name, Main: m.Key, MainName: m.Name, Short: s.Name}
}
