package web

import (
	"html"
	"html/template"
	"regexp"
	"strings"
)

// markdown renders a file's Markdown for the editor's preview. It is small on
// purpose: headings, paragraphs, lists, quotes, fenced code, pipe tables, rules
// and the inline forms below. Every piece of source text is escaped; the only
// markup in the output is what this function writes, and links are kept only
// for http(s) and mailto. Raw HTML in the source shows as text.
func markdown(src string) template.HTML {
	var b strings.Builder
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var para []string
	flush := func() {
		if len(para) > 0 {
			b.WriteString("<p>" + mdInline(strings.Join(para, " ")) + "</p>\n")
			para = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trim := strings.TrimSpace(line)
		switch {
		case trim == "":
			flush()
		case strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~"):
			flush()
			fence := trim[:3]
			var code []string
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), fence); i++ {
				code = append(code, lines[i])
			}
			b.WriteString(`<pre class="code"><code>` + html.EscapeString(strings.Join(code, "\n")) + "</code></pre>\n")
		case mdHeading.MatchString(trim):
			flush()
			m := mdHeading.FindStringSubmatch(trim)
			n := string(rune('0' + len(m[1])))
			b.WriteString("<h" + n + ">" + mdInline(strings.TrimRight(m[2], " #")) + "</h" + n + ">\n")
		case mdRule.MatchString(trim):
			flush()
			b.WriteString("<hr>\n")
		case strings.HasPrefix(trim, ">"):
			flush()
			var q []string
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">"); i++ {
				q = append(q, strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(lines[i]), ">"), " "))
			}
			i--
			b.WriteString("<blockquote>" + string(markdown(strings.Join(q, "\n"))) + "</blockquote>\n")
		case mdItem.MatchString(line):
			flush()
			ordered := mdOrdered.MatchString(line)
			tag := map[bool]string{true: "ol", false: "ul"}[ordered]
			b.WriteString("<" + tag + ">\n")
			for ; i < len(lines) && mdItem.MatchString(lines[i]); i++ {
				m := mdItem.FindStringSubmatch(lines[i])
				text := m[3]
				cls := ""
				for prefix, box := range map[string]string{"[ ] ": "☐ ", "[x] ": "☑ ", "[X] ": "☑ "} {
					if t, ok := strings.CutPrefix(text, prefix); ok {
						text, cls = box+t, ` class="task"`
					}
				}
				// continuation lines indented under the item
				for i+1 < len(lines) && strings.HasPrefix(lines[i+1], "  ") && strings.TrimSpace(lines[i+1]) != "" && !mdItem.MatchString(lines[i+1]) {
					i++
					text += "\n" + strings.TrimSpace(lines[i])
				}
				depth := len(m[1]) / 2
				b.WriteString("<li" + cls + mdDepth(depth) + ">" + mdInline(text) + "</li>\n")
			}
			i--
			b.WriteString("</" + tag + ">\n")
		case strings.HasPrefix(trim, "|") && i+1 < len(lines) && mdTableSep.MatchString(strings.TrimSpace(lines[i+1])):
			flush()
			b.WriteString(`<div class="md-table"><table><thead><tr>`)
			for _, c := range mdCells(trim) {
				b.WriteString("<th>" + mdInline(c) + "</th>")
			}
			b.WriteString("</tr></thead><tbody>\n")
			for i += 2; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
				b.WriteString("<tr>")
				for _, c := range mdCells(strings.TrimSpace(lines[i])) {
					b.WriteString("<td>" + mdInline(c) + "</td>")
				}
				b.WriteString("</tr>\n")
			}
			i--
			b.WriteString("</tbody></table></div>\n")
		default:
			para = append(para, trim)
		}
	}
	flush()
	return template.HTML(b.String())
}

var (
	mdHeading  = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	mdRule     = regexp.MustCompile(`^(?:-{3,}|\*{3,}|_{3,})$`)
	mdItem     = regexp.MustCompile(`^( *)([-*+]|\d{1,9}[.)])\s+(.*)$`)
	mdOrdered  = regexp.MustCompile(`^\s*\d`)
	mdTableSep = regexp.MustCompile(`^\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?$`)
	mdSpan     = regexp.MustCompile("`([^`\n]+)`|\\*\\*([^*\n]+)\\*\\*|__([^_\n]+)__|\\*([^*\n]+)\\*|\\[([^\\]\n]+)\\]\\(([^)\\s]+)\\)|(https?://[^\\s<]+[^\\s<.,;:!?)\\]'\"])")
)

func mdDepth(d int) string {
	if d <= 0 {
		return ""
	}
	return ` class="d` + string(rune('0'+min(d, 4))) + `"`
}

func mdCells(row string) []string {
	row = strings.TrimSuffix(strings.TrimPrefix(row, "|"), "|")
	cells := strings.Split(row, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

func mdSafeURL(u string) bool {
	l := strings.ToLower(u)
	return strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "mailto:")
}

// mdInline renders spans; text between them is escaped.
func mdInline(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range mdSpan.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(mdText(s[last:m[0]]))
		g := func(n int) string { return s[m[2*n]:m[2*n+1]] }
		switch {
		case m[2] >= 0:
			b.WriteString("<code>" + html.EscapeString(g(1)) + "</code>")
		case m[4] >= 0:
			b.WriteString("<strong>" + html.EscapeString(g(2)) + "</strong>")
		case m[6] >= 0:
			b.WriteString("<strong>" + html.EscapeString(g(3)) + "</strong>")
		case m[8] >= 0:
			b.WriteString("<em>" + html.EscapeString(g(4)) + "</em>")
		case m[10] >= 0:
			text, href := g(5), g(6)
			if mdSafeURL(href) {
				b.WriteString(`<a href="` + html.EscapeString(href) + `" target="_blank" rel="noopener noreferrer">` + html.EscapeString(text) + "</a>")
			} else {
				b.WriteString(html.EscapeString(text) + ` <span class="md-ref">(` + html.EscapeString(href) + ")</span>")
			}
		default:
			u := html.EscapeString(g(7))
			b.WriteString(`<a href="` + u + `" target="_blank" rel="noopener noreferrer">` + u + "</a>")
		}
		last = m[1]
	}
	b.WriteString(mdText(s[last:]))
	return b.String()
}

func mdText(s string) string {
	return strings.ReplaceAll(html.EscapeString(s), "\n", "<br>")
}
