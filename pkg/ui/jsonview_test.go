package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/lupohirp/brui/pkg/bru"
	"github.com/lupohirp/brui/pkg/theme"
)

const sampleJSON = `{"user":{"id":12345678901234567890,"name":"Alice","price":1.50,"active":true,"tags":["a","b"],"manager":null},"token":"eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhbGljZSIsImV4cCI6MTcwMDAwMDAwMCwic2NvcGUiOiJvcGVuaWQgcHJvZmlsZSBlbWFpbCJ9.c2lnbmF0dXJl","empty":{},"list":[]}`

func TestPrettyJSONKeepsOrderAndNumbers(t *testing.T) {
	out, ok := prettyJSON(sampleJSON)
	if !ok {
		t.Fatal("valid JSON not recognized")
	}
	if !strings.Contains(out, "\n  \"user\": {\n    \"id\": 12345678901234567890,") || !strings.Contains(out, `"price": 1.50`) {
		t.Fatalf("order/number formatting changed:\n%s", out)
	}
	if strings.Index(out, `"user"`) > strings.Index(out, `"token"`) {
		t.Fatal("key order changed")
	}
	if _, ok := prettyJSON("not json"); ok {
		t.Fatal("invalid JSON accepted")
	}
}

func TestJSONSpansClassification(t *testing.T) {
	p := jsonPalette{key: "K", str: "S", num: "N", lit: "L", punct: "P", variable: "V", plain: "-"}
	spans := jsonSpans(`  "name": "hi {{who}}", "n": -1.5e3, "ok": true, "z": null, "v": {{raw}}`, p)
	got := map[string]string{}
	for _, s := range spans {
		got[strings.TrimSpace(s.text)] = s.color
	}
	want := map[string]string{`"name"`: "K", `"hi`: "S", "{{who}}": "V", `-1.5e3`: "N", "true": "L", "null": "L", "{{raw}}": "V", ":": "P"}
	for text, color := range want {
		if got[text] != color {
			t.Errorf("%q: color %q, want %q (spans %+v)", text, got[text], color, spans)
		}
	}
}

func TestResponseBodyIsPrettyNumberedAndWrapped(t *testing.T) {
	resp := &bru.HTTPResponse{Status: 200, Body: sampleJSON, IsJSON: true}
	lines := renderBody(resp, theme.Mocha, 60)
	var plain []string
	for _, l := range lines {
		plain = append(plain, ansi.Strip(l))
		if w := ansi.StringWidth(l); w > 60 {
			t.Errorf("row wider than the pane (%d): %q", w, ansi.Strip(l))
		}
	}
	text := strings.Join(plain, "\n")
	if !strings.Contains(text, " 1  {") || !strings.Contains(text, `    "name": "Alice",`) {
		t.Fatalf("not pretty-printed with line numbers:\n%s", text)
	}
	// The long token wraps: continuation rows have no number and keep the indentation.
	var tokenRows []string
	for i, l := range plain {
		if strings.Contains(l, `"token"`) {
			tokenRows = plain[i : i+3]
		}
	}
	if len(tokenRows) < 3 || strings.TrimSpace(tokenRows[1][:4]) != "" || !strings.HasPrefix(strings.TrimLeft(tokenRows[1][4:], " "), "") {
		t.Fatalf("token not wrapped:\n%s", strings.Join(tokenRows, "\n"))
	}
	if !strings.Contains(strings.Join(tokenRows, ""), "c2lnbmF0dX") {
		t.Fatalf("wrapped token lost text:\n%s", text)
	}
	t.Logf("\n%s", text)
}

func TestNonJSONBodyWrapsWithoutColors(t *testing.T) {
	resp := &bru.HTTPResponse{Body: "<html>" + strings.Repeat("x", 100) + "</html>\nsecond"}
	lines := renderBody(resp, theme.Mocha, 40)
	if len(lines) < 4 || !strings.Contains(ansi.Strip(lines[len(lines)-1]), "2  second") {
		t.Fatalf("plain body: %q", lines)
	}
}

func TestBodyCacheReusedAcrossRenders(t *testing.T) {
	m := NewModel(&bru.BrunoCollection{RuntimeVariables: map[string]any{}})
	m.LastResponse = &bru.HTTPResponse{Body: sampleJSON, IsJSON: true}
	a := m.responseBodyLines(80)
	b := m.responseBodyLines(80)
	if &a[0] != &b[0] {
		t.Fatal("body re-rendered on every frame")
	}
	if c := m.responseBodyLines(70); &c[0] == &a[0] {
		t.Fatal("cache must depend on the width")
	}
}
