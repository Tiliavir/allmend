package markdown

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// ErrMarkmap is a ```markmap block that starts with front matter (---):
// Markmap options are not supported, so the block is rejected rather than
// drawn with the options shown as nodes.
var ErrMarkmap = errors.New("markmap front matter not supported")

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
func markmapTree(src string) (string, error) {
	if strings.HasPrefix(strings.TrimLeft(src, "\n"), "---") {
		return "", ErrMarkmap
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
		return "", b.err
	}
	if len(root.Children) == 1 {
		root = root.Children[0]
	}
	out, err := json.Marshal(root)
	if err != nil {
		return "", fmt.Errorf("markmap: %w", err)
	}
	return string(out), nil
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
