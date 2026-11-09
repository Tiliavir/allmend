// Package ui embeds the HTML templates and static assets.
package ui

import "embed"

// Templates holds templates/**.html.
//
//go:embed templates
var Templates embed.FS

// Static holds static/** (served under /static/).
//
//go:embed static
var Static embed.FS
