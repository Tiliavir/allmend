package markdown

import (
	"strings"
	"testing"

	"github.com/tiliavir/allmend/internal/pages"
)

func TestSafe(t *testing.T) {
	r, err := Render("<script>alert(1)</script>\n\n[x](javascript:alert(1)) <img src=x onerror=alert(1)>\n\n| a | b |\n|:-|-:|\n| 1 | 2 |\n", Tasks{})
	if err != nil {
		t.Fatal(err)
	}
	h := string(r.HTML)
	for _, bad := range []string{"<script", "javascript:", "onerror", "style="} {
		if strings.Contains(h, bad) {
			t.Errorf("output contains %q:\n%s", bad, h)
		}
	}
	if !strings.Contains(h, "<table>") {
		t.Error("table missing")
	}
}

func TestTasksAndMermaid(t *testing.T) {
	src := "# Einkauf\n\n- [ ] Milch & Brot\n- [x] Äpfel\n\n```mermaid\ngraph LR; A-->B\n```\n"
	r, err := Render(src, Tasks{Action: "/seiten/einkauf/haken", Version: 7, Text: pages.TaskText})
	if err != nil {
		t.Fatal(err)
	}
	h := string(r.HTML)
	for _, want := range []string{`name="line" value="2"`, `name="text" value="Milch &amp; Brot"`, `name="line" value="3"`, `aria-pressed="true"`, `name="version" value="7"`, `<pre class="mermaid">graph LR; A--&gt;B`} {
		if !strings.Contains(h, want) {
			t.Errorf("missing %q in\n%s", want, h)
		}
	}
	if !strings.Contains(h, `<h2 id="einkauf">Einkauf</h2>`) {
		t.Errorf("heading not shifted: %s", h)
	}
	if !r.HasMermaid {
		t.Error("mermaid not detected")
	}
	ro, _ := Render(src, Tasks{})
	if strings.Contains(string(ro.HTML), "<form") || !strings.Contains(string(ro.HTML), "disabled") {
		t.Error("read-only tasks must be disabled checkboxes")
	}
}
