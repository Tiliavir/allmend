package webui

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
)

// manifestFile is the web app manifest in ui/static. Its name and
// short_name are replaced by the configured app name, which Android shows
// under the icon of the installed app.
const manifestFile = "allmend.webmanifest"

func (s *Server) appName() string {
	if s.AppName == "" {
		return "Allmend"
	}
	return s.AppName
}

func (s *Server) manifest(w http.ResponseWriter, r *http.Request) {
	b, err := s.manifestJSON()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(b) //nolint:gosec // our own embedded asset plus the configured name
}

func (s *Server) manifestJSON() ([]byte, error) {
	raw, err := fs.ReadFile(s.Static, manifestFile)
	if err != nil {
		return nil, fmt.Errorf("webui: manifest: %w", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("webui: manifest: %w", err)
	}
	name, err := json.Marshal(s.appName())
	if err != nil {
		return nil, fmt.Errorf("webui: manifest: %w", err)
	}
	m["name"], m["short_name"] = name, name
	return json.MarshalIndent(m, "", "  ")
}
