package bru_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lupohirp/brui/pkg/bru"
)

const fixtureDir = "testdata/collection"

func readFixture(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func findRequest(t *testing.T, coll *bru.BrunoCollection, name string) *bru.BrunoRequest {
	t.Helper()
	var walk func([]*bru.BrunoFolder, []*bru.BrunoRequest) *bru.BrunoRequest
	walk = func(fs []*bru.BrunoFolder, rs []*bru.BrunoRequest) *bru.BrunoRequest {
		for _, r := range rs {
			if r.Name == name {
				return r
			}
		}
		for _, f := range fs {
			if r := walk(f.Folders, f.Requests); r != nil {
				return r
			}
		}
		return nil
	}
	r := walk(coll.Folders, coll.Requests)
	if r == nil {
		t.Fatalf("request %q not found", name)
	}
	return r
}

func loadFixture(t *testing.T) *bru.BrunoCollection {
	t.Helper()
	coll, err := bru.LoadCollection(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	return coll
}

// Saving an unmodified request must reproduce the file byte-for-byte.
func TestRequestRoundTripIsLossless(t *testing.T) {
	raw := readFixture(t, "users/Get User.bru")
	got := bru.SerializeRequest(bru.ParseRequest(raw))
	if got != raw {
		t.Fatalf("round trip changed the file.\n--- want\n%s\n--- got\n%s", raw, got)
	}
}

func TestCollectionAndEnvironmentRoundTripIsLossless(t *testing.T) {
	coll := loadFixture(t)
	if got, want := bru.SerializeCollectionBru(coll), readFixture(t, "collection.bru"); got != want {
		t.Fatalf("collection.bru changed.\n--- want\n%s\n--- got\n%s", want, got)
	}
	if got, want := bru.SerializeEnvironment(coll.Environments[0]), readFixture(t, "environments/Dev.bru"); got != want {
		t.Fatalf("environment changed.\n--- want\n%s\n--- got\n%s", want, got)
	}
}

func TestParseRequestFields(t *testing.T) {
	req := bru.ParseRequest(readFixture(t, "users/Get User.bru"))

	wantParams := []bru.BruParam{
		{Name: "expand", Value: "roles", Enabled: true},
		{Name: "page", Value: "1", Enabled: true},
		{Name: "debug", Value: "true", Enabled: false},
		{Name: "id", Value: "{{userId}}", Enabled: true, Type: bru.ParamTypePath},
	}
	if len(req.Params) != len(wantParams) {
		t.Fatalf("params: want %d, got %+v", len(wantParams), req.Params)
	}
	for i, p := range wantParams {
		if req.Params[i] != p {
			t.Errorf("param %d: want %+v, got %+v", i, p, req.Params[i])
		}
	}

	if len(req.Headers) != 2 || req.Headers[1] != (bru.BruHeader{Name: "X-Off", Value: "1", Enabled: false}) {
		t.Errorf("disabled header not parsed correctly: %+v", req.Headers)
	}
	if req.BodyType != "json" || !strings.HasPrefix(req.BodyContent, "{\n  \"filter\"") {
		t.Errorf("body should be the dedented body:json block, got type=%q content=%q", req.BodyType, req.BodyContent)
	}
	if !strings.Contains(req.BodyContent, `"braces } in strings {"`) {
		t.Errorf("braces inside the body broke block parsing: %q", req.BodyContent)
	}
	if !strings.HasPrefix(req.PreRequestScript, "if (!bru.getVar") {
		t.Errorf("script should be dedented, got %q", req.PreRequestScript)
	}
	if req.Docs != "Returns a user.\n\nSecond paragraph." {
		t.Errorf("docs: %q", req.Docs)
	}
}

// Editing managed fields must keep every block brui does not understand.
func TestEditPreservesUnknownBlocks(t *testing.T) {
	req := bru.ParseRequest(readFixture(t, "users/Get User.bru"))
	req.Method = bru.MethodPost
	req.Headers = append(req.Headers, bru.BruHeader{Name: "X-New", Value: "v", Enabled: true})
	req.BodyContent = "{\n\"typed\": {\n}\n}" // unindented, as typed in the editor
	req.PreRequestScript = ""

	out := bru.SerializeRequest(req)
	for _, keep := range []string{
		"auth:oauth2 {", "grant_type: password", "body:text {", "vars:pre-request {",
		"assert {", "tests {", "settings {", "tags: [", "~debug: true", "~X-Off: 1",
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("serialized request lost %q:\n%s", keep, out)
		}
	}
	if strings.Contains(out, "script:pre-request") || strings.Contains(out, "\nget {") {
		t.Errorf("removed/renamed blocks still present:\n%s", out)
	}

	back := bru.ParseRequest(out)
	if back.Method != bru.MethodPost || back.BodyContent != req.BodyContent || len(back.Headers) != 3 {
		t.Errorf("re-parse mismatch: method=%s headers=%d body=%q", back.Method, len(back.Headers), back.BodyContent)
	}
	if len(back.Params) != 4 {
		t.Errorf("params lost after save: %+v", back.Params)
	}
}

func TestNewRequestSerializesValidBru(t *testing.T) {
	req := &bru.BrunoRequest{Name: "New", Method: bru.MethodGet, URL: "https://x.io", BodyType: "json", BodyContent: "{\n  \n}"}
	out := bru.SerializeRequest(req)
	if !strings.Contains(out, "auth: inherit") {
		t.Errorf("empty auth mode should default to inherit:\n%s", out)
	}
	if back := bru.ParseRequest(out); back.BodyContent != req.BodyContent || back.URL != req.URL {
		t.Errorf("new request does not round trip: %+v", back)
	}
}

func TestLegacyFormBodyModeIsNormalized(t *testing.T) {
	req := bru.ParseRequest(readFixture(t, "users/legacy.bru"))
	if req.BodyType != "formUrlEncoded" || req.BodyContent != "a: 1\n~b: 2" {
		t.Fatalf("got type=%q content=%q", req.BodyType, req.BodyContent)
	}
	out := bru.SerializeRequest(req)
	if !strings.Contains(out, "body: formUrlEncoded") || !strings.Contains(out, "body:form-urlencoded {") {
		t.Errorf("body mode/block names wrong:\n%s", out)
	}
}

func TestEnvironmentEditsPreserveDisabledAndSecrets(t *testing.T) {
	env := loadFixture(t).Environments[0]
	if _, ok := env.Variables["~legacyUrl"]; ok {
		t.Fatal("disabled variable must not be active")
	}
	env.Variables["baseUrl"] = "https://new.example.com"
	delete(env.Variables, "userId")
	env.Variables["added"] = "x"

	want := `vars {
  baseUrl: https://new.example.com
  ~legacyUrl: https://old.example.com
  added: x
}

vars:secret [
  apiKey,
  clientSecret
]
`
	if got := bru.SerializeEnvironment(env); got != want {
		t.Fatalf("--- want\n%s\n--- got\n%s", want, got)
	}
}

func TestCollectionParse(t *testing.T) {
	coll := loadFixture(t)
	if coll.Name != "Fixture API" {
		t.Errorf("name from bruno.json: %q", coll.Name)
	}
	if coll.CollectionAuthType != "bearer" || coll.CollectionAuthBearerToken != "{{access_token}}" {
		t.Errorf("collection auth: %q %q", coll.CollectionAuthType, coll.CollectionAuthBearerToken)
	}
	if len(coll.CollectionHeaders) != 2 || coll.CollectionHeaders[1].Enabled {
		t.Errorf("collection headers: %+v", coll.CollectionHeaders)
	}
}

func TestBuildURL(t *testing.T) {
	coll := loadFixture(t)
	req := findRequest(t, coll, "Get User")

	// Query params already in the URL must not be duplicated; path params are substituted.
	if got, want := bru.BuildURL(req, coll), "https://dev.example.com/users/42?expand=roles&page=1"; got != want {
		t.Errorf("want %s, got %s", want, got)
	}

	// Params not present in the URL (hand-written files) are appended.
	r2 := &bru.BrunoRequest{URL: "https://x.io/a", Params: []bru.BruParam{{Name: "q", Value: "a b", Enabled: true}, {Name: "off", Value: "1"}}}
	if got, want := bru.BuildURL(r2, coll), "https://x.io/a?q=a+b"; got != want {
		t.Errorf("want %s, got %s", want, got)
	}
}

func TestSyncParamsAndURL(t *testing.T) {
	req := &bru.BrunoRequest{
		URL: "https://x.io/u/:id?a=1",
		Params: []bru.BruParam{
			{Name: "a", Value: "1", Enabled: true},
			{Name: "old", Value: "0", Enabled: false},
			{Name: "id", Value: "7", Enabled: true, Type: bru.ParamTypePath},
		},
	}
	req.URL = "https://x.io/u/:id?a=2&b=3"
	bru.SyncParamsFromURL(req)
	if len(req.Params) != 4 || req.Params[2].Name != "a" || req.Params[2].Value != "2" || req.Params[3].Name != "b" {
		t.Fatalf("params after URL edit: %+v", req.Params)
	}

	req.Params = []bru.BruParam{{Name: "z", Value: "9", Enabled: true}, {Name: "id", Value: "7", Enabled: true, Type: bru.ParamTypePath}}
	bru.SyncURLFromParams(req)
	if req.URL != "https://x.io/u/:id?z=9" {
		t.Fatalf("URL after params edit: %s", req.URL)
	}
}

// Script side effects must affect only the executed copy and runtime vars,
// never the loaded request or the environment that gets saved to disk.
func TestScriptMutationsDoNotLeakIntoSavedState(t *testing.T) {
	coll := loadFixture(t)
	orig := findRequest(t, coll, "Get User")
	env := coll.Environments[0]
	before := bru.SerializeRequest(orig)

	exec := orig.Clone()
	exec.PreRequestScript = `req.setHeader("X-Script", "1"); req.setUrl("https://changed"); bru.setEnvVar("token", "secret-value");`
	sr := bru.NewScriptRunner(coll)
	if err := sr.RunPreRequest(exec); err != nil {
		t.Fatal(err)
	}

	if exec.URL != "https://changed" {
		t.Fatalf("script did not run on the copy")
	}
	if got := bru.SerializeRequest(orig); got != before {
		t.Errorf("original request was modified by the script:\n%s", got)
	}
	if _, ok := env.Variables["token"]; ok {
		t.Errorf("setEnvVar leaked into the persisted environment")
	}
	if v, _ := bru.GetVariable("token", coll); v != "secret-value" {
		t.Errorf("setEnvVar value not visible to interpolation: %q", v)
	}
}

func TestParseBruBlocksLongLine(t *testing.T) {
	long := strings.Repeat("x", 200_000) // bufio.Scanner used to fail above 64KB
	blocks := bru.ParseBruBlocks("body:text {\n  " + long + "\n}\n")
	if len(blocks) != 1 || len(blocks[0].Content) != 1 || len(blocks[0].Content[0]) != len(long)+2 {
		t.Fatalf("long line not parsed: %d blocks", len(blocks))
	}
}

// Unchanged entries keep their exact original formatting (e.g. `key:` without
// the trailing space Bruno would write); only edited entries are reformatted.
func TestUnchangedEntriesKeepOriginalFormatting(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "Dev.bru")
	if err := os.WriteFile(p, []byte("vars {\n  empty:\n  host:   spaced\n  changed: a\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "environments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(p, filepath.Join(dir, "environments", "Dev.bru")); err != nil {
		t.Fatal(err)
	}
	coll, err := bru.LoadCollection(dir)
	if err != nil {
		t.Fatal(err)
	}
	env := coll.Environments[0]
	env.Variables["changed"] = "b"
	want := "vars {\n  empty:\n  host:   spaced\n  changed: b\n}\n"
	if got := bru.SerializeEnvironment(env); got != want {
		t.Fatalf("--- want\n%q\n--- got\n%q", want, got)
	}

	req := bru.ParseRequest("headers {\n  A:1\n  B: 2\n}\n")
	req.Headers[1].Value = "3"
	if out := bru.SerializeRequest(req); !strings.Contains(out, "headers {\n  A:1\n  B: 3\n}") {
		t.Fatalf("unchanged header line was reformatted:\n%s", out)
	}
}
