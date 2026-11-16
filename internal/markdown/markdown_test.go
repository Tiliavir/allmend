package markdown

import (
	"errors"
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

func TestMarkmap(t *testing.T) {
	src := "```markmap\n# Ich\n\n## Arbeit\n\n- ~~Colygon~~\n  - 17(?).03.2013 <b>x</b>\n- [Helvetia](https://example.org)\n\n## Wohnen\n\nWollbach\n```\n"
	r, err := Render(src, Tasks{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.HasMarkmap || r.HasMermaid {
		t.Fatalf("flags: %+v", r)
	}
	tree, err := markmapTree("# Ich\n\n## Arbeit\n\n- ~~Colygon~~\n  - 17(?).03.2013 <b>x</b>\n- [Helvetia](https://example.org)\n\n## Wohnen\n\nWollbach\n")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"content":"Ich","children":[{"content":"Arbeit","children":[{"content":"<del>Colygon</del>","children":[{"content":"17(?).03.2013 <!-- raw HTML omitted -->x<!-- raw HTML omitted -->"}]},{"content":"<a href=\"https://example.org\">Helvetia</a>"}]},{"content":"Wohnen","children":[{"content":"<p>Wollbach</p>"}]}]}`
	tree = strings.NewReplacer(`\u003c`, "<", `\u003e`, ">").Replace(tree) // json.Marshal escapes < and >
	if tree != want {
		t.Errorf("tree:\n%s\nwant\n%s", tree, want)
	}
	h := string(r.HTML)
	if !strings.HasPrefix(h, `<pre class="markmap" data-markmap="{&#34;content&#34;:&#34;Ich&#34;`) || !strings.Contains(h, ">\n# Ich\n") && !strings.Contains(h, "># Ich\n") {
		t.Errorf("html: %s", h)
	}
	if strings.Contains(h, "<b>") || strings.Contains(h, "<del>") {
		t.Errorf("unescaped HTML in attribute or source: %s", h)
	}
}

func TestMarkmapRoots(t *testing.T) {
	tree, err := markmapTree("- A\n- B\n")
	if err != nil {
		t.Fatal(err)
	}
	if tree != `{"content":"","children":[{"content":"A"},{"content":"B"}]}` {
		t.Errorf("two roots: %s", tree)
	}
	if _, err := Render("```markmap\n---\nmarkmap:\n  colorFreezeLevel: 2\n---\n# Ich\n```\n", Tasks{}); !errors.Is(err, ErrMarkmap) {
		t.Errorf("front matter: %v", err)
	}
}
