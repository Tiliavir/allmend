package webui

import "html/template"

// icons are trusted constants (stroke SVG, 24×24), never user input.
var icons = map[string]string{
	"logo":   `<svg viewBox="0 0 32 32" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true"><path d="M4 25a12 12 0 0 1 24 0"/><path d="M10 25c0-4 1-7 3-9M16 25v-11M22 25c0-4-1-7-3-9"/><path d="M3 25h26"/></svg>`,
	"bike":   `<circle cx="6" cy="16" r="4"/><circle cx="18" cy="16" r="4"/><path d="M6 16l4-8h5l3 8M10 8l2 8h-6M14 5h3"/>`,
	"chart":  `<path d="M4 20V4M4 20h16M8 16l4-5 3 3 5-7"/>`,
	"route":  `<circle cx="6" cy="18" r="2"/><circle cx="18" cy="6" r="2"/><path d="M8 18h7a3 3 0 0 0 0-6H9a3 3 0 0 1 0-6h7"/>`,
	"pin":    `<path d="M12 21s-6-6.2-6-11a6 6 0 0 1 12 0c0 4.8-6 11-6 11z"/><circle cx="12" cy="10" r="2"/>`,
	"page":   `<path d="M7 3h7l4 4v14H7zM14 3v4h4M10 12h5M10 16h5"/>`,
	"ship":   `<path d="M3 15h18l-3 5H6zM12 4v11M12 5l6 7h-6"/>`,
	"coins":  `<ellipse cx="9" cy="7" rx="5" ry="2.5"/><path d="M4 7v4c0 1.4 2.2 2.5 5 2.5s5-1.1 5-2.5V7M10 15.4c.9 1.2 2.9 2.1 5 2.1 2.8 0 5-1.1 5-2.5v-4c0-1.4-2.2-2.5-5-2.5-.3 0-.7 0-1 .1M20 15v4c0 1.4-2.2 2.5-5 2.5s-5-1.1-5-2.5v-4"/>`,
	"check":  `<path d="M5 12l4 4 10-10"/>`,
	"pdf":    `<path d="M7 3h7l4 4v14H7zM14 3v4h4M9.5 15h5"/>`,
	"cal":    `<rect x="4" y="5" width="16" height="15" rx="2"/><path d="M4 10h16M9 3v4M15 3v4"/>`,
	"book":   `<path d="M5 4h6a2 2 0 0 1 2 2v14a2 2 0 0 0-2-2H5zM19 4h-6a2 2 0 0 0-2 2v14a2 2 0 0 1 2-2h6z"/>`,
	"server": `<rect x="4" y="4" width="16" height="7" rx="1.5"/><rect x="4" y="13" width="16" height="7" rx="1.5"/><path d="M8 7.5h.01M8 16.5h.01"/>`,
	"home":   `<path d="M4 11l8-7 8 7v9h-5v-6H9v6H4z"/>`,
	"link":   `<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"/>`,
	"google": `<path d="M20 12.2c0-.6-.1-1.2-.2-1.8H12v3.4h4.5a3.9 3.9 0 0 1-1.7 2.5v2h2.7c1.6-1.4 2.5-3.6 2.5-6.1z"/><path d="M12 20.5c2.3 0 4.2-.8 5.5-2.1l-2.7-2a5 5 0 0 1-7.5-2.7H4.5v2.1A8.5 8.5 0 0 0 12 20.5z"/><path d="M7.3 13.7a5 5 0 0 1 0-3.4V8.2H4.5a8.5 8.5 0 0 0 0 7.6z"/><path d="M12 6.9c1.3 0 2.4.4 3.3 1.3l2.4-2.4A8.5 8.5 0 0 0 4.5 8.2l2.8 2.1A5 5 0 0 1 12 6.9z"/>`,
}

var iconLabels = map[string]string{
	"link": "Link", "check": "Haken", "cal": "Kalender", "pdf": "Dokument", "book": "Buch",
	"server": "Server", "home": "Haus", "page": "Seite", "chart": "Diagramm",
}

func icon(name string) template.HTML {
	s, ok := icons[name]
	if !ok {
		s = icons["link"]
	}
	if name == "logo" {
		return template.HTML(s) //nolint:gosec // constant markup
	}
	return template.HTML(`<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` + s + `</svg>`) //nolint:gosec // constant markup
}
