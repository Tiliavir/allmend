package markdown

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// MarkmapError is a ```markmap block the page cannot show; Reason is German
// for the edit form.
type MarkmapError struct{ Reason string }

func (e *MarkmapError) Error() string { return "markmap: " + e.Reason }

// markmapOptions are the Markmap options a block may set in its front matter,
// in Markmap's own format:
//
//	---
//	markmap:
//	  initialExpandLevel: -1
//	---
//
// Each is an integer with its allowed minimum; anything else is an error.
var markmapOptions = map[string]int{
	"initialExpandLevel": -1, // -1: all levels open
	"colorFreezeLevel":   0,
	"maxWidth":           0, // 0: no limit
}

// splitFrontMatter returns the options of a block (JSON, "" without front
// matter) and the Markdown after it.
func splitFrontMatter(src string) (string, string, error) {
	lines := strings.Split(strings.TrimLeft(src, "\n"), "\n")
	if strings.TrimSpace(lines[0]) != "---" {
		return "", src, nil
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return "", "", &MarkmapError{"Die Einstellungen am Anfang des Blocks werden nicht mit --- abgeschlossen."}
	}
	if end < 3 || strings.TrimSpace(lines[1]) != "markmap:" {
		return "", "", &MarkmapError{"Die Einstellungen müssen mit „markmap:“ beginnen, darunter eingerückt z. B. „initialExpandLevel: -1“."}
	}
	opts := map[string]int{}
	for _, line := range lines[2:end] {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || !strings.HasPrefix(line, " ") {
			return "", "", &MarkmapError{fmt.Sprintf("Unerwartete Zeile in den Einstellungen: „%s“.", strings.TrimSpace(line))}
		}
		key = strings.TrimSpace(key)
		low, known := markmapOptions[key]
		if !known {
			return "", "", &MarkmapError{fmt.Sprintf("Unbekannte Einstellung „%s“. Möglich: initialExpandLevel, colorFreezeLevel, maxWidth.", key)}
		}
		if _, dup := opts[key]; dup {
			return "", "", &MarkmapError{fmt.Sprintf("Einstellung „%s“ steht doppelt da.", key)}
		}
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < low || (key == "initialExpandLevel" && n == 0) {
			return "", "", &MarkmapError{fmt.Sprintf("„%s“ braucht eine ganze Zahl ab %d (bei initialExpandLevel: -1 für alle Ebenen oder ab 1).", key, low)}
		}
		opts[key] = n
	}
	out, err := json.Marshal(opts)
	if err != nil {
		return "", "", fmt.Errorf("markmap options: %w", err)
	}
	return string(out), strings.Join(lines[end+1:], "\n"), nil
}

// mmNode is the node shape markmap-view expects: HTML content and children.
type mmNode struct {
	Content  string    `json:"content"`
	Children []*mmNode `json:"children,omitempty"`
}

// markmapTree turns the Markdown of a ```markmap block into the JSON tree
// markmap-view draws, as markmap does: headings nest by level, list items by
// indentation, any other block is a leaf under the heading before it. One
// top-level node is the root; several get an empty root above them. Node
// content is goldmark HTML in safe mode (raw HTML dropped, URLs filtered).
// Options from the front matter come back as JSON ("" without).
func markmapTree(src string) (tree, options string, err error) {
	options, src, err = splitFrontMatter(src)
	if err != nil {
		return "", "", err
	}
	source := []byte(src)
	md := goldmark.New(goldmark.WithExtensions(
		extension.NewTable(extension.WithTableCellAlignMethod(extension.TableCellAlignNone)),
		extension.Strikethrough, extension.Linkify, extension.TaskList,
	))
	b := &mmBuilder{md: md, source: source}
	root := &mmNode{}
	type open struct {
		level int
		node  *mmNode
	}
	stack := []open{{0, root}}
	for c := md.Parser().Parse(text.NewReader(source)).FirstChild(); c != nil; c = c.NextSibling() {
		if h, ok := c.(*ast.Heading); ok {
			for stack[len(stack)-1].level >= h.Level {
				stack = stack[:len(stack)-1]
			}
			n := &mmNode{Content: b.inline(h)}
			top := stack[len(stack)-1].node
			top.Children = append(top.Children, n)
			stack = append(stack, open{h.Level, n})
			continue
		}
		b.add(stack[len(stack)-1].node, c)
	}
	if b.err != nil {
		return "", "", b.err
	}
	if len(root.Children) == 1 {
		root = root.Children[0]
	}
	out, err := json.Marshal(root)
	if err != nil {
		return "", "", fmt.Errorf("markmap: %w", err)
	}
	return string(out), options, nil
}

type mmBuilder struct {
	md     goldmark.Markdown
	source []byte
	err    error
}

// add appends block c below parent: each list item as a node, anything else
// as one leaf.
func (b *mmBuilder) add(parent *mmNode, c ast.Node) {
	if _, ok := c.(*ast.List); !ok {
		parent.Children = append(parent.Children, &mmNode{Content: b.html(c)})
		return
	}
	for item := c.FirstChild(); item != nil; item = item.NextSibling() {
		n := &mmNode{}
		rest := item.FirstChild()
		if rest != nil && (rest.Kind() == ast.KindParagraph || rest.Kind() == ast.KindTextBlock) {
			n.Content = b.inline(rest)
			rest = rest.NextSibling()
		}
		for ; rest != nil; rest = rest.NextSibling() {
			b.add(n, rest)
		}
		parent.Children = append(parent.Children, n)
	}
}

// inline renders the children of n (the text of a heading or paragraph).
func (b *mmBuilder) inline(n ast.Node) string {
	var buf bytes.Buffer
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if err := b.md.Renderer().Render(&buf, b.source, c); err != nil && b.err == nil {
			b.err = fmt.Errorf("markmap: %w", err)
		}
	}
	return strings.TrimSpace(buf.String())
}

func (b *mmBuilder) html(n ast.Node) string {
	var buf bytes.Buffer
	if err := b.md.Renderer().Render(&buf, b.source, n); err != nil && b.err == nil {
		b.err = fmt.Errorf("markmap: %w", err)
	}
	return strings.TrimSpace(buf.String())
}
