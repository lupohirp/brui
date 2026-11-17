package bru

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestColl(base string) *BrunoCollection {
	return &BrunoCollection{RuntimeVariables: map[string]any{"base": base}}
}

func withScriptTimeout(t *testing.T, d time.Duration) {
	old := ScriptTimeout
	ScriptTimeout = d
	t.Cleanup(func() { ScriptTimeout = old })
}

func TestScriptTimeoutInterruptsInfiniteLoop(t *testing.T) {
	withScriptTimeout(t, 200*time.Millisecond)
	req := &BrunoRequest{Method: MethodGet, URL: "http://127.0.0.1:1", PreRequestScript: "while (true) {}"}

	start := time.Now()
	res := RunWithScripts(context.Background(), req, newTestColl(""), RunOptions{})
	if !errors.Is(res.Err, ErrScriptTimeout) {
		t.Fatalf("want ErrScriptTimeout, got %v", res.Err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("timeout took too long: %s", time.Since(start))
	}
}

func TestCancelInterruptsScript(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	req := &BrunoRequest{Method: MethodGet, URL: "http://127.0.0.1:1", PreRequestScript: "while (true) {}"}
	if res := RunWithScripts(ctx, req, newTestColl(""), RunOptions{}); !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", res.Err)
	}
}

func TestCancelAbortsHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // hang until the client goes away
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	res := RunWithScripts(ctx, &BrunoRequest{Method: MethodGet, URL: "{{base}}/slow"}, newTestColl(srv.URL), RunOptions{})
	if !errors.Is(res.Err, context.Canceled) || time.Since(start) > 3*time.Second {
		t.Fatalf("want quick cancellation, got %v after %s", res.Err, time.Since(start))
	}
}

func TestPreRequestErrorAbortsRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()

	req := &BrunoRequest{Method: MethodGet, URL: "{{base}}", PreRequestScript: `throw new Error("boom")`}
	res := RunWithScripts(context.Background(), req, newTestColl(srv.URL), RunOptions{})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "boom") || res.Response != nil {
		t.Fatalf("want pre-request error, got err=%v resp=%v", res.Err, res.Response)
	}
	if hits.Load() != 0 {
		t.Fatal("request must not be sent when the pre-request script fails")
	}
}

func TestRunWithScriptsOnCopies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"echo": %q}`, r.Header.Get("X-Token"))
	}))
	defer srv.Close()
	withScriptTimeout(t, 50*time.Millisecond)

	live := newTestColl(srv.URL)
	live.Environments = []*BrunoEnvironment{{Name: "Dev", Variables: map[string]string{"k": "v"}}}
	live.ActiveEnvironment = "Dev"
	orig := &BrunoRequest{
		Method:             MethodGet,
		URL:                "{{base}}/x",
		PreRequestScript:   `bru.setVar("tok", "abc"); req.setHeader("X-Token", bru.getVar("tok")); console.log("pre");`,
		PostResponseScript: `bru.setEnvVar("echoed", res.body.echo);`,
	}
	live.Requests = []*BrunoRequest{orig}

	exec, snap := orig.Clone(), live.CloneForExecution()
	time.Sleep(10 * time.Millisecond)
	res := RunWithScripts(context.Background(), exec, snap, RunOptions{})
	time.Sleep(80 * time.Millisecond) // a stale timeout must not leak into later runs
	if res.Err != nil || res.PostScriptErr != nil {
		t.Fatalf("unexpected errors: %v / %v", res.Err, res.PostScriptErr)
	}
	if res.RuntimeVariables["echoed"] != "abc" || res.RuntimeVariables["tok"] != "abc" {
		t.Errorf("runtime vars not returned: %v", res.RuntimeVariables)
	}
	if _, ok := live.RuntimeVariables["tok"]; ok || len(orig.Headers) != 0 {
		t.Error("live collection/request must be untouched until the caller merges results")
	}
	if len(res.Logs) != 1 || res.Logs[0].Type != "info" {
		t.Errorf("logs not captured: %+v", res.Logs)
	}
}

func TestNestedRunRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"path": %q, "after": %q}`, r.URL.Path, r.Header.Get("X-After"))
	}))
	defer srv.Close()

	coll := newTestColl(srv.URL)
	login := &BrunoRequest{Name: "Login", RelativePath: "auth/login.bru", Method: MethodPost, URL: "{{base}}/login",
		PostResponseScript: `bru.setVar("token", "t-" + res.body.path);`}
	coll.Folders = []*BrunoFolder{{Name: "auth", Requests: []*BrunoRequest{login}}}

	main := &BrunoRequest{Method: MethodGet, URL: "{{base}}/main",
		PreRequestScript: `const r = await bru.runRequest("auth/login"); req.setHeader("X-After", String(r.status));`}
	res := RunWithScripts(context.Background(), main, coll, RunOptions{})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if res.RuntimeVariables["token"] != "t-/login" {
		t.Errorf("nested post-response did not run: %v", res.RuntimeVariables)
	}
	// req must point back to the outer request after runRequest returns.
	if !strings.Contains(res.Response.Body, `"path": "/main"`) || !strings.Contains(res.Response.Body, `"after": "200"`) {
		t.Errorf("outer request not restored: %s", res.Response.Body)
	}

	// The nested run must not disarm the outer script's timeout.
	withScriptTimeout(t, 300*time.Millisecond)
	main.PreRequestScript = `bru.runRequest("auth/login"); while (true) {}`
	if res := RunWithScripts(context.Background(), main, coll, RunOptions{}); !errors.Is(res.Err, ErrScriptTimeout) {
		t.Fatalf("outer timeout lost after nested run: %v", res.Err)
	}
}

func TestConsoleFormatting(t *testing.T) {
	sr := NewScriptRunner(newTestColl(""))
	req := &BrunoRequest{PreRequestScript: `
		console.log("hi", 42, true, null, undefined);
		console.log({a: 1, b: [1, 2]});
		const c = {}; c.self = c; console.warn(c);
		console.error(new Error("bad"));`}
	if err := sr.RunPreRequest(req); err != nil {
		t.Fatal(err)
	}
	logs := sr.GetLogs()
	want := []string{
		"hi 42 true null undefined",
		"{\n  \"a\": 1,\n  \"b\": [\n    1,\n    2\n  ]\n}",
		"[object Object]",
		"Error: bad",
	}
	if len(logs) != len(want) {
		t.Fatalf("logs: %+v", logs)
	}
	for i, w := range want {
		if logs[i].Message != w {
			t.Errorf("log %d: want %q, got %q", i, w, logs[i].Message)
		}
	}
}
