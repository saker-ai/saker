// HTML→Markdown conversion used by WebFetchTool. Only a curated subset of
// elements is supported; the goal is a readable summary, not faithful
// round-tripping. The fetcher and tool surface live in sibling files.
package toolbuiltin

import (
	"fmt"
	"html"
	"strings"

	xhtml "golang.org/x/net/html"
)

// htmlToMarkdown converts a limited subset of HTML nodes into Markdown.
func htmlToMarkdown(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	node, err := xhtml.Parse(strings.NewReader(trimmed))
	if err != nil {
		return strings.TrimSpace(html.UnescapeString(trimmed))
	}
	builder := &markdownBuilder{}
	builder.walk(node)
	result := strings.TrimSpace(builder.String())
	if result == "" {
		return strings.TrimSpace(html.UnescapeString(trimmed))
	}
	return result
}

type markdownBuilder struct {
	strings.Builder
	listStack  []listContext
	linkStack  []string
	inPre      bool
	quoteDepth int
	tables     []*tableBuilder
}

type tableBuilder struct {
	rows       [][]string
	currentRow []string
	cellBuf    strings.Builder
	inCell     bool
	inHeader   bool
}

type listContext struct {
	ordered bool
	index   int
}

func (m *markdownBuilder) walk(n *xhtml.Node) {
	switch n.Type {
	case xhtml.TextNode:
		m.writeText(n.Data)
	case xhtml.ElementNode:
		if shouldSkipNode(n.Data) {
			return
		}
		m.handleStart(n)
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			m.walk(child)
		}
		m.handleEnd(n)
	default:
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			m.walk(child)
		}
	}
}

func shouldSkipNode(name string) bool {
	lower := strings.ToLower(name)
	return lower == "script" || lower == "style" || lower == "noscript"
}

func (m *markdownBuilder) handleStart(n *xhtml.Node) {
	name := strings.ToLower(n.Data)
	switch name {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		m.ensureBlankLine()
		level := nameToHeadingLevel(name)
		m.WriteString(strings.Repeat("#", level))
		m.WriteString(" ")
	case "p", "div", "section", "article":
		m.ensureBlankLine()
	case "br":
		m.WriteString("\n")
	case "pre":
		if !m.inPre {
			m.ensureBlankLine()
			m.WriteString("```\n")
			m.inPre = true
		}
	case "code":
		if !m.inPre {
			m.WriteString("`")
		}
	case "strong", "b":
		m.WriteString("**")
	case "em", "i":
		m.WriteString("*")
	case "ul":
		m.listStack = append(m.listStack, listContext{})
		m.ensureBlankLine()
	case "ol":
		m.listStack = append(m.listStack, listContext{ordered: true})
		m.ensureBlankLine()
	case "li":
		m.startListItem()
	case "a":
		if href := attrValue(n, "href"); href != "" {
			m.linkStack = append(m.linkStack, href)
			m.WriteString("[")
		}
	case "img":
		alt := attrValue(n, "alt")
		src := attrValue(n, "src")
		if src != "" {
			if alt == "" {
				alt = src
			}
			m.WriteString("![")
			m.WriteString(alt)
			m.WriteString("](")
			m.WriteString(src)
			m.WriteString(")")
		}
	case "table":
		m.ensureBlankLine()
		m.tables = append(m.tables, &tableBuilder{})
	case "thead":
		if len(m.tables) > 0 {
			m.tables[len(m.tables)-1].inHeader = true
		}
	case "tbody":
		if len(m.tables) > 0 {
			m.tables[len(m.tables)-1].inHeader = false
		}
	case "tr":
		if len(m.tables) > 0 {
			m.tables[len(m.tables)-1].currentRow = nil
		}
	case "th", "td":
		if len(m.tables) > 0 {
			tb := m.tables[len(m.tables)-1]
			tb.cellBuf.Reset()
			tb.inCell = true
		}
	case "blockquote":
		m.ensureBlankLine()
		m.quoteDepth++
	case "hr":
		m.ensureBlankLine()
		m.WriteString("---\n")
	case "details":
		m.ensureBlankLine()
	case "summary":
		m.WriteString("**")
	}
}

func (m *markdownBuilder) handleEnd(n *xhtml.Node) {
	name := strings.ToLower(n.Data)
	switch name {
	case "h1", "h2", "h3", "h4", "h5", "h6", "p", "div", "section", "article":
		m.WriteString("\n")
	case "pre":
		if m.inPre {
			if !strings.HasSuffix(m.String(), "\n") {
				m.WriteString("\n")
			}
			m.WriteString("```\n")
			m.inPre = false
		}
	case "code":
		if !m.inPre {
			m.WriteString("`")
		}
	case "strong", "b":
		m.WriteString("**")
	case "em", "i":
		m.WriteString("*")
	case "ul", "ol":
		if len(m.listStack) > 0 {
			m.listStack = m.listStack[:len(m.listStack)-1]
		}
		m.WriteString("\n")
	case "a":
		if len(m.linkStack) > 0 {
			href := m.linkStack[len(m.linkStack)-1]
			m.linkStack = m.linkStack[:len(m.linkStack)-1]
			m.WriteString("](")
			m.WriteString(href)
			m.WriteString(")")
		}
	case "th", "td":
		if len(m.tables) > 0 {
			tb := m.tables[len(m.tables)-1]
			if tb.inCell {
				tb.currentRow = append(tb.currentRow, strings.TrimSpace(tb.cellBuf.String()))
				tb.inCell = false
			}
		}
	case "tr":
		if len(m.tables) > 0 {
			tb := m.tables[len(m.tables)-1]
			if len(tb.currentRow) > 0 {
				tb.rows = append(tb.rows, tb.currentRow)
				if tb.inHeader {
					sep := make([]string, len(tb.currentRow))
					for i := range sep {
						sep[i] = "---"
					}
					tb.rows = append(tb.rows, sep)
				}
			}
			tb.currentRow = nil
		}
	case "table":
		if len(m.tables) > 0 {
			tb := m.tables[len(m.tables)-1]
			m.tables = m.tables[:len(m.tables)-1]
			m.renderTable(tb)
		}
	case "thead", "tbody":
		// handled via inHeader flag toggle in handleStart
	case "blockquote":
		if m.quoteDepth > 0 {
			m.quoteDepth--
		}
		m.WriteString("\n")
	case "details":
		m.WriteString("\n")
	case "summary":
		m.WriteString("**\n")
	}
}

func (m *markdownBuilder) writeText(text string) {
	if strings.TrimSpace(text) == "" && !m.inPre {
		return
	}
	if m.inPre {
		m.WriteString(text)
		return
	}
	// If inside a table cell, write to cell buffer instead of main builder.
	if len(m.tables) > 0 {
		tb := m.tables[len(m.tables)-1]
		if tb.inCell {
			cleaned := collapseSpaces(html.UnescapeString(text))
			tb.cellBuf.WriteString(cleaned)
			return
		}
	}
	cleaned := collapseSpaces(html.UnescapeString(text))
	if cleaned == "" {
		return
	}
	current := m.String()
	if m.Len() > 0 && !strings.HasSuffix(current, "\n") && !strings.HasSuffix(current, "**") {
		m.WriteString(" ")
	}
	if m.quoteDepth > 0 {
		prefix := strings.Repeat("> ", m.quoteDepth)
		// Prefix at start of content if we're at the beginning of a line.
		if m.Len() == 0 || strings.HasSuffix(m.String(), "\n") {
			m.WriteString(prefix)
		}
		cleaned = strings.ReplaceAll(cleaned, "\n", "\n"+prefix)
	}
	m.WriteString(cleaned)
}

func (m *markdownBuilder) renderTable(tb *tableBuilder) {
	if len(tb.rows) == 0 {
		return
	}
	m.ensureBlankLine()
	for _, row := range tb.rows {
		m.WriteString("| ")
		m.WriteString(strings.Join(row, " | "))
		m.WriteString(" |\n")
	}
}

func (m *markdownBuilder) ensureBlankLine() {
	if m.Len() == 0 {
		return
	}
	if !strings.HasSuffix(m.String(), "\n\n") {
		if strings.HasSuffix(m.String(), "\n") {
			m.WriteString("\n")
		} else {
			m.WriteString("\n\n")
		}
	}
}

func (m *markdownBuilder) startListItem() {
	if len(m.listStack) == 0 {
		m.listStack = append(m.listStack, listContext{})
	}
	ctx := &m.listStack[len(m.listStack)-1]
	indent := strings.Repeat("  ", len(m.listStack)-1)
	marker := "- "
	if ctx.ordered {
		ctx.index++
		marker = fmt.Sprintf("%d. ", ctx.index)
	}
	if !strings.HasSuffix(m.String(), "\n") {
		m.WriteString("\n")
	}
	m.WriteString(indent)
	m.WriteString(marker)
}

func nameToHeadingLevel(name string) int {
	switch name {
	case "h1":
		return 1
	case "h2":
		return 2
	case "h3":
		return 3
	case "h4":
		return 4
	case "h5":
		return 5
	default:
		return 6
	}
}

func attrValue(n *xhtml.Node, key string) string {
	lower := strings.ToLower(key)
	for _, attr := range n.Attr {
		if strings.ToLower(attr.Key) == lower {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func collapseSpaces(input string) string {
	fields := strings.Fields(input)
	return strings.Join(fields, " ")
}

func summariseMarkdown(md string) string {
	if md == "" {
		return ""
	}
	lines := strings.Split(md, "\n")
	if len(lines) > markdownSnippetMaxLines {
		lines = lines[:markdownSnippetMaxLines]
		lines = append(lines, "...")
	}
	trimmed := strings.TrimSpace(strings.Join(lines, "\n"))
	return trimmed
}
