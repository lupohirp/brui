package bru

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// BruBlock is one top-level block of a .bru file, e.g. `body:json { ... }` or
// `vars:secret [ ... ]`. Content holds the raw lines exactly as they appear in
// the file (indentation included) so unknown blocks can be written back
// verbatim.
type BruBlock struct {
	Name    string
	SubName string
	Header  string // original header text without the opening bracket, e.g. "body:json"
	Array   bool   // true for list blocks delimited by [ ]
	Content []string
	Before  []string // lines between the previous block and this one (blank lines, comments)
}

func (b BruBlock) key() string {
	if b.SubName != "" {
		return b.Name + ":" + b.SubName
	}
	return b.Name
}

// ParseBruBlocks splits a .bru document into its top-level blocks.
//
// Following the Bruno grammar, a block is closed by a `}` (or `]` for list
// blocks) at column 0. Block contents are always indented, so braces inside
// JSON bodies or scripts never terminate the block early.
func ParseBruBlocks(raw string) []BruBlock {
	var blocks []BruBlock
	var cur *BruBlock
	var gap []string
	closer := ""

	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if cur == nil {
			t := strings.TrimSpace(line)
			array := false
			switch {
			case strings.HasSuffix(t, "{"):
			case strings.HasSuffix(t, "["):
				array = true
			default:
				gap = append(gap, line)
				continue
			}
			header := strings.TrimSpace(t[:len(t)-1])
			b := BruBlock{Header: header, Array: array, Before: gap}
			gap = nil
			if parts := strings.Fields(header); len(parts) > 0 {
				b.Name, b.SubName, _ = strings.Cut(parts[0], ":")
				if b.SubName == "" && len(parts) > 1 {
					b.SubName = parts[1]
				}
			}
			cur = &b
			closer = "}"
			if array {
				closer = "]"
			}
			continue
		}
		if strings.TrimRight(line, " \t") == closer {
			blocks = append(blocks, *cur)
			cur = nil
			continue
		}
		cur.Content = append(cur.Content, line)
	}
	if cur != nil { // unterminated block: keep what we have rather than dropping it
		blocks = append(blocks, *cur)
	}
	return blocks
}

// extraTrailingNewlines returns the newlines a file has beyond the final one,
// so saving does not change how the file ends.
func extraTrailingNewlines(raw string) string {
	n := len(raw) - len(strings.TrimRight(raw, "\n"))
	if n > 1 {
		return strings.Repeat("\n", n-1)
	}
	return ""
}

// dedent removes the 2-space block indentation Bruno adds to text blocks
// (bodies, scripts, docs).
func dedent(lines []string) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimPrefix(l, "  ")
	}
	return strings.Join(out, "\n")
}

// indent is the inverse of dedent. Empty lines stay empty.
func indent(text string) []string {
	text = strings.TrimSuffix(text, "\n")
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = "  " + l
		}
	}
	return lines
}

type dictEntry struct {
	Key     string
	Value   string
	Enabled bool
}

// parseDictLine parses a `key: value` line of a dictionary block. A leading
// `~` marks the entry as disabled; keys may be double-quoted.
func parseDictLine(line string) (dictEntry, bool) {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "//") {
		return dictEntry{}, false
	}
	e := dictEntry{Enabled: true}
	if strings.HasPrefix(t, "~") {
		e.Enabled = false
		t = strings.TrimSpace(t[1:])
	}
	if strings.HasPrefix(t, `"`) {
		end := strings.Index(t[1:], `"`)
		if end < 0 {
			return dictEntry{}, false
		}
		e.Key = t[1 : end+1]
		rest := strings.TrimSpace(t[end+2:])
		if !strings.HasPrefix(rest, ":") {
			return dictEntry{}, false
		}
		e.Value = strings.TrimSpace(rest[1:])
		return e, e.Key != ""
	}
	k, v, ok := strings.Cut(t, ":")
	if !ok {
		return dictEntry{}, false
	}
	e.Key = strings.TrimSpace(k)
	e.Value = strings.TrimSpace(v)
	return e, true // Bruno accepts empty keys; callers skip them where meaningless
}

func formatDictLine(key, value string, enabled bool) string {
	if strings.ContainsAny(key, `:"`) || strings.HasPrefix(key, "~") || strings.TrimSpace(key) != key {
		key = strconv.Quote(key)
	}
	prefix := "  "
	if !enabled {
		prefix += "~"
	}
	return prefix + key + ": " + value // Bruno keeps the space even for empty values
}

func parseKeyValue(line string) (string, string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "//") {
		return "", ""
	}
	parts := strings.SplitN(trimmed, ":", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	parts = strings.Fields(trimmed)
	if len(parts) >= 2 {
		return parts[0], strings.Join(parts[1:], " ")
	}
	return parts[0], ""
}

// setKV sets `key: value` in a key/value block, replacing the existing line in
// place (so ordering and unrelated keys are preserved) or appending it.
func setKV(lines []string, key, value string) []string {
	out := append([]string(nil), lines...)
	for i, l := range out {
		if k, v := parseKeyValue(l); k == key {
			if v != value { // unchanged lines are kept byte-for-byte
				out[i] = formatDictLine(key, value, true)
			}
			return out
		}
	}
	return append(out, formatDictLine(key, value, true))
}

func hasKey(lines []string, key string) bool {
	for _, l := range lines {
		if k, _ := parseKeyValue(l); k == key {
			return true
		}
	}
	return false
}

// Body modes as written in the method block (`body: formUrlEncoded`) differ
// from the corresponding block names (`body:form-urlencoded`).
var bodyModeToBlock = map[string]string{
	"formUrlEncoded": "form-urlencoded",
	"multipartForm":  "multipart-form",
}

func normalizeBodyMode(mode string) string {
	for m, b := range bodyModeToBlock {
		if mode == b {
			return m
		}
	}
	if mode == "" {
		return "none"
	}
	return mode
}

func bodyBlockName(mode string) string {
	if b, ok := bodyModeToBlock[mode]; ok {
		return b
	}
	return mode
}

var httpMethods = map[string]bool{
	"get": true, "post": true, "put": true, "patch": true,
	"delete": true, "options": true, "head": true,
}

// requestBlockKey maps a block to the identity used when merging managed
// blocks into the original file.
func requestBlockKey(b BruBlock) string {
	if httpMethods[b.Name] && b.SubName == "" {
		return "http"
	}
	if b.Name == "query" && b.SubName == "" { // legacy name of params:query
		return "params:query"
	}
	return b.key()
}

func ParseRequestFile(filePath, relPath string) (*BrunoRequest, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	filename := filepath.Base(filePath)
	req := ParseRequest(string(data))
	req.ID = relPath
	req.Filename = filename
	req.FilePath = filePath
	req.RelativePath = relPath
	if req.Name == "" {
		req.Name = strings.TrimSuffix(filename, ".bru")
	}
	return req, nil
}

// ParseRequest parses the content of a request .bru file.
func ParseRequest(raw string) *BrunoRequest {
	blocks := ParseBruBlocks(raw)
	req := &BrunoRequest{
		Seq:       1,
		Method:    MethodGet,
		Headers:   []BruHeader{},
		Params:    []BruParam{},
		BodyType:  "none",
		AuthType:  "inherit",
		rawBlocks: blocks,
		rawTail:   extraTrailingNewlines(raw),
	}

	bodies := map[string]string{}
	var bodyOrder []string
	bodySpecified := false

	for _, block := range blocks {
		switch requestBlockKey(block) {
		case "meta":
			for _, line := range block.Content {
				k, v := parseKeyValue(line)
				if k == "name" && v != "" {
					req.Name = v
				} else if k == "seq" {
					if s, err := strconv.Atoi(v); err == nil {
						req.Seq = s
					}
				}
			}

		case "http":
			req.Method = HttpMethod(strings.ToUpper(block.Name))
			for _, line := range block.Content {
				k, v := parseKeyValue(line)
				switch k {
				case "url":
					req.URL = v
				case "body":
					req.BodyType = normalizeBodyMode(v)
					bodySpecified = true
				case "auth":
					req.AuthType = v
				}
			}

		case "headers":
			for _, line := range block.Content {
				if e, ok := parseDictLine(line); ok {
					req.Headers = append(req.Headers, BruHeader{Name: e.Key, Value: e.Value, Enabled: e.Enabled})
				}
			}

		case "params:query", "params:path":
			typ := ParamTypeQuery
			if block.SubName == "path" {
				typ = ParamTypePath
			}
			for _, line := range block.Content {
				if e, ok := parseDictLine(line); ok {
					req.Params = append(req.Params, BruParam{Name: e.Key, Value: e.Value, Enabled: e.Enabled, Type: typ})
				}
			}

		case "auth:bearer":
			for _, line := range block.Content {
				if k, v := parseKeyValue(line); k == "token" {
					req.AuthBearerToken = v
				}
			}

		case "auth:oauth2":
			req.AuthOAuth2 = parseOAuth2Block(block.Content)

		case "auth:basic":
			for _, line := range block.Content {
				switch k, v := parseKeyValue(line); k {
				case "username":
					req.AuthBasicUser = v
				case "password":
					req.AuthBasicPassword = v
				}
			}

		case "script:pre-request":
			req.PreRequestScript = dedent(block.Content)
		case "script:post-response":
			req.PostResponseScript = dedent(block.Content)
		case "docs":
			req.Docs = dedent(block.Content)

		default:
			if block.Name == "body" && block.SubName != "" {
				bodies[block.SubName] = dedent(block.Content)
				bodyOrder = append(bodyOrder, block.SubName)
			}
		}
	}

	if !bodySpecified && len(bodyOrder) > 0 {
		req.BodyType = normalizeBodyMode(bodyOrder[0])
	}
	req.BodyContent = bodies[bodyBlockName(req.BodyType)]
	return req
}

// SerializeRequest renders a request back to .bru. Blocks brui edits are
// regenerated (in their original position when present); every other block
// (tests, assert, vars, auth:oauth2, other body modes, settings, ...) is
// written back verbatim so saving never drops data.
func SerializeRequest(req *BrunoRequest) string {
	bodyMode := normalizeBodyMode(req.BodyType)
	authMode := req.AuthType
	if authMode == "" {
		authMode = "inherit"
	}
	orig := func(key string) *BruBlock {
		for i := range req.rawBlocks {
			if requestBlockKey(req.rawBlocks[i]) == key {
				return &req.rawBlocks[i]
			}
		}
		return nil
	}
	content := func(b *BruBlock) []string {
		if b == nil {
			return nil
		}
		return b.Content
	}

	m := newManagedBlocks()

	// meta: update in place, keeping unknown keys (type, tags, ...)
	meta := setKV(content(orig("meta")), "name", req.Name)
	if !hasKey(meta, "type") {
		meta = append(meta, formatDictLine("type", "http", true))
	}
	meta = setKV(meta, "seq", strconv.Itoa(req.Seq))
	m.set("meta", &BruBlock{Header: "meta", Content: meta})

	method := strings.ToLower(string(req.Method))
	if method == "" {
		method = "get"
	}
	httpLines := content(orig("http"))
	httpLines = setKV(httpLines, "url", req.URL)
	httpLines = setKV(httpLines, "body", bodyMode)
	httpLines = setKV(httpLines, "auth", authMode)
	m.set("http", &BruBlock{Header: method, Content: httpLines})

	var query, path []dictEntry
	for _, p := range req.Params {
		e := dictEntry{Key: p.Name, Value: p.Value, Enabled: p.Enabled}
		if p.Type == ParamTypePath {
			path = append(path, e)
		} else {
			query = append(query, e)
		}
	}
	m.setDict("params:query", orig("params:query"), query)
	m.setDict("params:path", orig("params:path"), path)

	var headers []dictEntry
	for _, h := range req.Headers {
		headers = append(headers, dictEntry{Key: h.Name, Value: h.Value, Enabled: h.Enabled})
	}
	m.setDict("headers", orig("headers"), headers)

	if req.AuthBearerToken != "" {
		m.set("auth:bearer", &BruBlock{Header: "auth:bearer", Content: setKV(content(orig("auth:bearer")), "token", req.AuthBearerToken)})
	} else {
		m.set("auth:bearer", nil)
	}
	if req.AuthBasicUser != "" || req.AuthBasicPassword != "" {
		lines := setKV(content(orig("auth:basic")), "username", req.AuthBasicUser)
		lines = setKV(lines, "password", req.AuthBasicPassword)
		m.set("auth:basic", &BruBlock{Header: "auth:basic", Content: lines})
	} else {
		m.set("auth:basic", nil)
	}

	if bodyMode != "none" {
		key := "body:" + bodyBlockName(bodyMode)
		m.setText(key, orig(key), req.BodyContent)
	}
	m.setText("script:pre-request", orig("script:pre-request"), req.PreRequestScript)
	m.setText("script:post-response", orig("script:post-response"), req.PostResponseScript)
	m.setText("docs", orig("docs"), req.Docs)

	return m.render(req.rawBlocks, requestBlockKey) + req.rawTail
}

// managedBlocks collects the blocks brui regenerates, keyed by block identity.
// A nil entry means "remove this block if present".
type managedBlocks struct {
	blocks map[string]*BruBlock
	order  []string
}

func newManagedBlocks() *managedBlocks {
	return &managedBlocks{blocks: map[string]*BruBlock{}}
}

func (m *managedBlocks) set(key string, b *BruBlock) {
	if _, ok := m.blocks[key]; !ok {
		m.order = append(m.order, key)
	}
	m.blocks[key] = b
}

// setDict manages a dictionary block. Entries identical to one in the original
// block reuse its exact line, so only changed entries are reformatted.
func (m *managedBlocks) setDict(key string, orig *BruBlock, entries []dictEntry) {
	if len(entries) == 0 {
		m.set(key, nil)
		return
	}
	origLines := map[dictEntry][]string{}
	if orig != nil {
		for _, l := range orig.Content {
			if e, ok := parseDictLine(l); ok {
				origLines[e] = append(origLines[e], l)
			}
		}
	}
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		if q := origLines[e]; len(q) > 0 {
			lines = append(lines, q[0])
			origLines[e] = q[1:]
			continue
		}
		lines = append(lines, formatDictLine(e.Key, e.Value, e.Enabled))
	}
	m.set(key, &BruBlock{Header: key, Content: lines})
}

// setText manages a free-text block (body, script, docs). If the text is
// unchanged the original lines are kept byte-for-byte.
func (m *managedBlocks) setText(key string, orig *BruBlock, text string) {
	if text == "" {
		m.set(key, nil)
		return
	}
	if orig != nil && dedent(orig.Content) == text {
		m.set(key, orig)
		return
	}
	m.set(key, &BruBlock{Header: key, Content: indent(text)})
}

// render merges managed blocks into the original block sequence: managed blocks
// replace their original counterpart in place, unmanaged blocks are copied
// verbatim, and new managed blocks are appended.
func (m *managedBlocks) render(original []BruBlock, keyOf func(BruBlock) string) string {
	var sb strings.Builder
	// emit writes a block preceded by the original separator lines; new blocks
	// get a single blank line, like Bruno writes them.
	emit := func(b BruBlock, before []string, isNew bool) {
		if isNew {
			before = nil
			if sb.Len() > 0 {
				before = []string{""}
			}
		}
		for _, l := range before {
			sb.WriteString(l + "\n")
		}
		sb.WriteString(renderBlock(b))
	}
	emitted := map[string]bool{}
	for _, b := range original {
		k := keyOf(b)
		nb, managed := m.blocks[k]
		if !managed {
			emit(b, b.Before, false)
			continue
		}
		if emitted[k] {
			continue
		}
		emitted[k] = true
		if nb != nil {
			emit(*nb, b.Before, false)
		}
	}
	for _, k := range m.order {
		if !emitted[k] && m.blocks[k] != nil {
			emit(*m.blocks[k], nil, true)
		}
	}
	return sb.String()
}

func renderBlock(b BruBlock) string {
	open, close := "{", "}"
	if b.Array {
		open, close = "[", "]"
	}
	var sb strings.Builder
	sb.WriteString(b.Header + " " + open + "\n")
	for _, l := range b.Content {
		sb.WriteString(l + "\n")
	}
	sb.WriteString(close + "\n")
	return sb.String()
}

// sortedKeys returns the keys of a string map in lexical order.
func sortedKeys(mp map[string]string) []string {
	keys := make([]string, 0, len(mp))
	for k := range mp {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
