// Package markdown renders page Markdown to safe HTML with goldmark: GFM
// tables, task lists, strikethrough and autolinks; raw HTML is dropped and
// dangerous link schemes are neutralised (goldmark's safe mode). Task list
// checkboxes become small forms so they can be ticked without JavaScript;
// ```mermaid blocks are left for the client-side Mermaid renderer.
package markdown

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// Tasks controls how checklist items render.
type Tasks struct {
	Action  string // form action for ticking; empty: read-only checkboxes
	Version int    // page version the view is based on
	Text    func(line string) (string, bool)
}

// Result is the rendered page.
type Result struct {
	HTML       template.HTML
	HasMermaid bool
}

// Render converts Markdown to HTML.
func Render(src string, tasks Tasks) (Result, error) {
	source := []byte(src)
	r := &extras{source: source, tasks: tasks}
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.NewTable(extension.WithTableCellAlignMethod(extension.TableCellAlignNone)),
			extension.Strikethrough, extension.Linkify, extension.TaskList,
		),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(r, 100))),
	)
	var buf bytes.Buffer
	if err := md.Convert(source, &buf); err != nil {
		return Result{}, fmt.Errorf("markdown.Render: %w", err)
	}
	return Result{HTML: template.HTML(buf.String()), HasMermaid: r.mermaid}, nil //nolint:gosec // goldmark safe mode: raw HTML omitted, URLs filtered
}

type extras struct {
	source  []byte
	tasks   Tasks
	mermaid bool
}

func (r *extras) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(east.KindTaskCheckBox, r.task)
	reg.Register(ast.KindFencedCodeBlock, r.code)
	reg.Register(ast.KindHeading, r.heading)
}

// heading shifts levels down by one: the page title is the only <h1>.
func (r *extras) heading(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	h := n.(*ast.Heading)
	level := min(h.Level+1, 6)
	if !entering {
		_, _ = fmt.Fprintf(w, "</h%d>\n", level)
		return ast.WalkContinue, nil
	}
	if id, ok := h.AttributeString("id"); ok {
		if b, ok := id.([]byte); ok {
			_, _ = fmt.Fprintf(w, `<h%d id="%s">`, level, html.EscapeString(string(b)))
			return ast.WalkContinue, nil
		}
	}
	_, _ = fmt.Fprintf(w, "<h%d>", level)
	return ast.WalkContinue, nil
}

func (r *extras) lineOf(n ast.Node) int {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Lines().Len() > 0 {
			return bytes.Count(r.source[:p.Lines().At(0).Start], []byte("\n"))
		}
	}
	return -1
}

func (r *extras) task(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	cb := n.(*east.TaskCheckBox)
	label := "offen"
	if cb.IsChecked {
		label = "erledigt"
	}
	line := r.lineOf(n)
	text, ok := "", false
	if line >= 0 && r.tasks.Text != nil {
		lines := strings.Split(string(r.source), "\n")
		if line < len(lines) {
			text, ok = r.tasks.Text(lines[line])
		}
	}
	checked := ""
	if cb.IsChecked {
		checked = " checked"
	}
	if r.tasks.Action == "" || !ok {
		_, _ = fmt.Fprintf(w, `<input type="checkbox" disabled%s aria-label="%s"> `, checked, label)
		return ast.WalkContinue, nil
	}
	_, _ = fmt.Fprintf(w, `<form class="task" method="post" action="%s" id="aufgabe-%d"><input type="hidden" name="version" value="%d"><input type="hidden" name="line" value="%d"><input type="hidden" name="text" value="%s"><button type="submit" class="task-box" aria-pressed="%t" aria-label="%s: %s umschalten"></button></form>`,
		html.EscapeString(r.tasks.Action), line, r.tasks.Version, line, html.EscapeString(text), cb.IsChecked, label, html.EscapeString(text))
	return ast.WalkContinue, nil
}

func (r *extras) code(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	b := n.(*ast.FencedCodeBlock)
	lang := string(b.Language(src))
	var body strings.Builder
	for i := 0; i < b.Lines().Len(); i++ {
		seg := b.Lines().At(i)
		body.Write(seg.Value(src))
	}
	text := html.EscapeString(body.String())
	if lang == "mermaid" {
		r.mermaid = true
		_, _ = fmt.Fprintf(w, "<pre class=\"mermaid\">%s</pre>\n", text)
		return ast.WalkSkipChildren, nil
	}
	if lang != "" {
		_, _ = fmt.Fprintf(w, "<pre><code class=\"language-%s\">%s</code></pre>\n", html.EscapeString(lang), text)
	} else {
		_, _ = fmt.Fprintf(w, "<pre><code>%s</code></pre>\n", text)
	}
	return ast.WalkSkipChildren, nil
}
