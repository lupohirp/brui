package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lupohirp/brui/pkg/bru"
	"github.com/lupohirp/brui/pkg/theme"
)

// Response body view: JSON is re-indented and syntax-highlighted, long
// lines wrap under their own indentation, and lines are numbered.

// span is a run of text drawn in one style.
type span struct {
	text  string
	color string
	bold  bool
}

type jsonPalette struct {
	key, str, num, lit, punct, variable, plain string
}

func paletteFor(t theme.Theme) jsonPalette {
	return jsonPalette{key: t.Accent, str: t.Success, num: t.Warn, lit: t.Put, punct: t.Subtext, variable: t.Patch, plain: t.Fg}
}

// prettyJSON re-indents valid JSON with 2 spaces, keeping key order and the
// exact number formatting; anything else is returned unchanged.
func prettyJSON(body string) (string, bool) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(strings.TrimSpace(body)), "", "  "); err != nil {
		return body, false
	}
	return buf.String(), true
}

// jsonSpans tokenizes one line of JSON. It is tolerant: request bodies may
// contain {{variables}} where JSON values go, or be incomplete while edited.
func jsonSpans(line string, p jsonPalette) []span {
	var out []span
	add := func(text, color string, bold bool) {
		if text == "" {
			return
		}
		if n := len(out); n > 0 && out[n-1].color == color && out[n-1].bold == bold {
			out[n-1].text += text
			return
		}
		out = append(out, span{text, color, bold})
	}
	// strSpans colors a string, highlighting {{variables}} inside it.
	strSpans := func(s, color string) {
		for len(s) > 0 {
			loc := varRegex.FindStringIndex(s)
			if loc == nil {
				add(s, color, false)
				return
			}
			add(s[:loc[0]], color, false)
			add(s[loc[0]:loc[1]], p.variable, true)
			s = s[loc[1]:]
		}
	}

	for i := 0; i < len(line); {
		c := line[i]
		switch {
		case c == ' ' || c == '\t':
			j := i
			for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
				j++
			}
			add(line[i:j], p.plain, false)
			i = j
		case c == '"':
			j := i + 1
			for j < len(line) {
				if line[j] == '\\' {
					j += 2
					continue
				}
				if line[j] == '"' {
					j++
					break
				}
				j++
			}
			j = min(j, len(line))
			k := j
			for k < len(line) && line[k] == ' ' {
				k++
			}
			color := p.str
			if k < len(line) && line[k] == ':' {
				color = p.key
			}
			strSpans(line[i:j], color)
			i = j
		case c == '{' && strings.HasPrefix(line[i:], "{{"):
			if loc := varRegex.FindStringIndex(line[i:]); loc != nil && loc[0] == 0 {
				add(line[i:i+loc[1]], p.variable, true)
				i += loc[1]
				continue
			}
			add("{", p.punct, false)
			i++
		case strings.ContainsRune("{}[],:", rune(c)):
			add(string(c), p.punct, false)
			i++
		case c == '-' || (c >= '0' && c <= '9'):
			j := i + 1
			for j < len(line) && strings.IndexByte("0123456789.eE+-", line[j]) >= 0 {
				j++
			}
			add(line[i:j], p.num, false)
			i = j
		default:
			matched := false
			for _, lit := range []string{"true", "false", "null"} {
				if strings.HasPrefix(line[i:], lit) {
					add(lit, p.lit, false)
					i += len(lit)
					matched = true
					break
				}
			}
			if matched {
				continue
			}
			j := i + 1
			for j < len(line) && strings.IndexByte(" \t\"{}[],:", line[j]) < 0 {
				j++
			}
			add(line[i:j], p.plain, false)
			i = j
		}
	}
	return out
}

func renderSpans(spans []span) string {
	var sb strings.Builder
	for _, s := range spans {
		st := lipgloss.NewStyle().Foreground(lipgloss.Color(s.color))
		if s.bold {
			st = st.Bold(true)
		}
		sb.WriteString(st.Render(s.text))
	}
	return sb.String()
}

// wrapSpans breaks a line into rows of at most width cells. Continuation
// rows start with the line's own indentation plus two spaces.
func wrapSpans(spans []span, width int) [][]span {
	if width < 8 {
		width = 8
	}
	total := 0
	for _, s := range spans {
		total += ansi.StringWidth(s.text)
	}
	if total <= width {
		return [][]span{spans}
	}
	indent := 0
	if len(spans) > 0 {
		indent = len(spans[0].text) - len(strings.TrimLeft(spans[0].text, " \t"))
	}
	contIndent := min(indent+2, width/2)

	var rows [][]span
	var row []span
	used := 0
	newRow := func() {
		rows = append(rows, row)
		row = []span{{text: strings.Repeat(" ", contIndent)}}
		used = contIndent
	}
	for _, s := range spans {
		text := s.text
		for text != "" {
			// Take as many runes as fit in the remaining width.
			cut, w := 0, 0
			for cut < len(text) {
				r, size := utf8.DecodeRuneInString(text[cut:])
				rw := ansi.StringWidth(string(r))
				if used+w+rw > width {
					break
				}
				w += rw
				cut += size
			}
			if cut == 0 {
				if used == contIndent && len(rows) > 0 || used == 0 {
					_, size := utf8.DecodeRuneInString(text) // never loop on a too-wide rune
					cut = size
					w = ansi.StringWidth(text[:cut])
				} else {
					newRow()
					continue
				}
			}
			row = append(row, span{text[:cut], s.color, s.bold})
			used += w
			text = text[cut:]
			if text != "" {
				newRow()
			}
		}
	}
	rows = append(rows, row)
	return rows
}

// bodyCache keeps the rendered response body; the view is redrawn on every
// mouse move, and re-indenting/highlighting a large body each time is slow.
type bodyCache struct {
	resp  *bru.HTTPResponse
	theme string
	width int
	lines []string
}

// responseBodyLines returns the rendered body rows for a pane width.
func (m Model) responseBodyLines(width int) []string {
	resp := m.LastResponse
	if c := m.bodyCache; c != nil && c.resp == resp && c.theme == m.Theme.Name && c.width == width {
		return c.lines
	}
	lines := renderBody(resp, m.Theme, width)
	if m.bodyCache != nil {
		*m.bodyCache = bodyCache{resp: resp, theme: m.Theme.Name, width: width, lines: lines}
	}
	return lines
}

func renderBody(resp *bru.HTTPResponse, t theme.Theme, width int) []string {
	body := resp.Body
	isJSON := false
	if resp.IsJSON {
		body, isJSON = prettyJSON(body)
	}
	body = strings.ReplaceAll(body, "\r\n", "\n")
	src := strings.Split(strings.TrimRight(body, "\n"), "\n")

	numW := len(fmt.Sprint(len(src)))
	gutter := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Border))
	textW := width - numW - 3 // " 12 " + text
	p := paletteFor(t)

	var out []string
	for i, line := range src {
		line = strings.ReplaceAll(line, "\t", "    ")
		var spans []span
		if isJSON {
			spans = jsonSpans(line, p)
		} else {
			spans = []span{{text: line, color: t.Fg}}
		}
		for j, row := range wrapSpans(spans, textW) {
			num := strings.Repeat(" ", numW)
			if j == 0 {
				num = fmt.Sprintf("%*d", numW, i+1)
			}
			out = append(out, " "+gutter.Render(num)+"  "+renderSpans(row))
		}
	}
	return out
}

// highlightJSONText colors a request body (no re-indent: it is the file's
// content) line by line.
func highlightJSONText(text string, t theme.Theme) []string {
	p := paletteFor(t)
	var out []string
	for _, line := range strings.Split(text, "\n") {
		out = append(out, renderSpans(jsonSpans(strings.ReplaceAll(line, "\t", "    "), p)))
	}
	return out
}
