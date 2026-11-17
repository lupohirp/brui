package bru

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

var awaitKeywordRe = regexp.MustCompile(`\bawait\s+`)

// ScriptTimeout bounds the run time of each top-level script, including any
// nested bru.runRequest calls, so an infinite loop cannot hang brui.
var ScriptTimeout = 30 * time.Second

// ErrScriptTimeout is returned when a script exceeds ScriptTimeout.
var ErrScriptTimeout = errors.New("script timed out")

type ScriptRunner struct {
	vm    *goja.Runtime
	coll  *BrunoCollection
	logs  []ConsoleLogEntry
	cur   *BrunoRequest // request currently bound to the `req` global
	ctx   context.Context
	depth int // nesting level of script runs (bru.runRequest re-enters)
}

func NewScriptRunner(coll *BrunoCollection) *ScriptRunner {
	return NewScriptRunnerContext(context.Background(), coll)
}

// NewScriptRunnerContext returns a runner whose scripts and HTTP calls are
// aborted when ctx is cancelled.
func NewScriptRunnerContext(ctx context.Context, coll *BrunoCollection) *ScriptRunner {
	sr := &ScriptRunner{
		vm:   goja.New(),
		coll: coll,
		logs: []ConsoleLogEntry{},
		ctx:  ctx,
	}
	sr.initEnvironment()
	return sr
}

// run executes a user script. The outermost run arms the timeout and the
// cancellation watcher, which interrupt the VM; nested runs share them.
func (sr *ScriptRunner) run(script string) error {
	if sr.depth == 0 {
		if err := sr.ctx.Err(); err != nil {
			return err
		}
		// finished guards against a late timer/cancel interrupting a later run.
		var mu sync.Mutex
		finished := false
		interrupt := func(v any) {
			mu.Lock()
			defer mu.Unlock()
			if !finished {
				sr.vm.Interrupt(v)
			}
		}
		timer := time.AfterFunc(ScriptTimeout, func() { interrupt(ErrScriptTimeout) })
		stop := make(chan struct{})
		go func() {
			select {
			case <-sr.ctx.Done():
				interrupt(sr.ctx.Err())
			case <-stop:
			}
		}()
		defer func() {
			mu.Lock()
			finished = true
			mu.Unlock()
			timer.Stop()
			close(stop)
			sr.vm.ClearInterrupt()
		}()
	}
	sr.depth++
	defer func() { sr.depth-- }()

	_, err := sr.vm.RunString(wrapUserScript(script))
	var ie *goja.InterruptedError
	if errors.As(err, &ie) {
		if cause, ok := ie.Value().(error); ok {
			return cause
		}
	}
	return err
}

func (sr *ScriptRunner) GetLogs() []ConsoleLogEntry {
	return sr.logs
}

func (sr *ScriptRunner) ClearLogs() {
	sr.logs = []ConsoleLogEntry{}
}

func (sr *ScriptRunner) log(logType, msg string) {
	sr.logs = append(sr.logs, ConsoleLogEntry{
		Timestamp: time.Now().Format("15:04:05"),
		Type:      logType,
		Message:   msg,
	})
}

func (sr *ScriptRunner) findRequest(targetPath string) *BrunoRequest {
	if sr.coll == nil {
		return nil
	}
	norm := strings.TrimSuffix(strings.TrimSpace(targetPath), ".bru")
	normLower := strings.ToLower(norm)

	var searchFolder func(folders []*BrunoFolder, reqs []*BrunoRequest) *BrunoRequest
	searchFolder = func(folders []*BrunoFolder, reqs []*BrunoRequest) *BrunoRequest {
		for _, r := range reqs {
			relNoExt := strings.TrimSuffix(r.RelativePath, ".bru")
			if r.RelativePath == targetPath || relNoExt == norm ||
				strings.ToLower(relNoExt) == normLower ||
				strings.ToLower(r.Name) == normLower {
				return r
			}
		}
		for _, f := range folders {
			if found := searchFolder(f.Folders, f.Requests); found != nil {
				return found
			}
		}
		return nil
	}
	return searchFolder(sr.coll.Folders, sr.coll.Requests)
}

func wrapUserScript(script string) string {
	cleaned := awaitKeywordRe.ReplaceAllString(script, "")
	return "(function() {\n" + cleaned + "\n})()"
}

func (sr *ScriptRunner) initEnvironment() {
	// console object
	consoleObj := sr.vm.NewObject()
	consoleObj.Set("log", func(call goja.FunctionCall) goja.Value {
		sr.log("info", sr.formatArgs(call.Arguments))
		return goja.Undefined()
	})
	consoleObj.Set("info", func(call goja.FunctionCall) goja.Value {
		sr.log("info", sr.formatArgs(call.Arguments))
		return goja.Undefined()
	})
	consoleObj.Set("warn", func(call goja.FunctionCall) goja.Value {
		sr.log("warn", sr.formatArgs(call.Arguments))
		return goja.Undefined()
	})
	consoleObj.Set("error", func(call goja.FunctionCall) goja.Value {
		sr.log("error", sr.formatArgs(call.Arguments))
		return goja.Undefined()
	})
	sr.vm.Set("console", consoleObj)

	// bru object
	bruObj := sr.vm.NewObject()
	bruObj.Set("getEnvVar", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		varName := call.Arguments[0].String()
		if val, ok := GetVariable(varName, sr.coll); ok {
			return sr.vm.ToValue(val)
		}
		return goja.Undefined()
	})
	bruObj.Set("setEnvVar", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) >= 2 {
			// Stored as a runtime variable (which takes precedence over the
			// environment) instead of mutating the environment itself, so values
			// set by scripts (e.g. tokens) are never written to disk on save.
			k := call.Arguments[0].String()
			v := call.Arguments[1].String()
			sr.coll.RuntimeVariables[k] = v
		}
		return goja.Undefined()
	})
	bruObj.Set("getVar", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		k := call.Arguments[0].String()
		if val, ok := sr.coll.RuntimeVariables[k]; ok {
			return sr.vm.ToValue(val)
		}
		return goja.Undefined()
	})
	bruObj.Set("setVar", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) >= 2 {
			k := call.Arguments[0].String()
			v := call.Arguments[1].Export()
			sr.coll.RuntimeVariables[k] = v
		}
		return goja.Undefined()
	})
	bruObj.Set("runRequest", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		targetPath := call.Arguments[0].String()
		subReq := sr.findRequest(targetPath)
		if subReq == nil {
			sr.log("error", fmt.Sprintf("[bru.runRequest] Request not found: %s", targetPath))
			return goja.Undefined()
		}
		// Run on a copy so the sub-request's scripts cannot alter the loaded
		// request, and restore the caller's req/res bindings afterwards.
		subReq = subReq.Clone()
		outer, outerRes := sr.cur, sr.vm.Get("res")
		defer func() {
			if outer != nil {
				sr.bindReqObject(outer)
			}
			if outerRes != nil {
				sr.vm.Set("res", outerRes)
			}
		}()
		_ = sr.RunPreRequest(subReq)
		subResp, err := ExecuteRequestContext(sr.ctx, subReq, sr.coll)
		if err != nil {
			sr.log("error", fmt.Sprintf("[bru.runRequest] %s failed: %v", targetPath, err))
			return goja.Undefined()
		}
		_ = sr.RunPostResponse(subReq, subResp)
		var bodyVal any = subResp.Body
		if subResp.IsJSON {
			var parsed any
			if json.Unmarshal([]byte(subResp.Body), &parsed) == nil {
				bodyVal = parsed
			}
		}
		return sr.vm.ToValue(map[string]any{
			"status":  subResp.Status,
			"headers": subResp.Headers,
			"body":    bodyVal,
		})
	})
	sr.vm.Set("bru", bruObj)
}

func (sr *ScriptRunner) bindReqObject(req *BrunoRequest) {
	sr.cur = req
	reqObj := sr.vm.NewObject()
	reqObj.Set("getUrl", func(call goja.FunctionCall) goja.Value {
		return sr.vm.ToValue(Interpolate(req.URL, sr.coll))
	})
	reqObj.Set("setUrl", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) > 0 {
			req.URL = call.Arguments[0].String()
		}
		return goja.Undefined()
	})
	reqObj.Set("getMethod", func(call goja.FunctionCall) goja.Value {
		return sr.vm.ToValue(string(req.Method))
	})
	reqObj.Set("setMethod", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) > 0 {
			req.Method = HttpMethod(strings.ToUpper(call.Arguments[0].String()))
		}
		return goja.Undefined()
	})
	reqObj.Set("getHeader", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		name := strings.ToLower(call.Arguments[0].String())
		for _, h := range req.Headers {
			if strings.ToLower(h.Name) == name {
				return sr.vm.ToValue(Interpolate(h.Value, sr.coll))
			}
		}
		return goja.Undefined()
	})
	reqObj.Set("setHeader", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) >= 2 {
			name := call.Arguments[0].String()
			val := call.Arguments[1].String()
			for i := range req.Headers {
				if strings.EqualFold(req.Headers[i].Name, name) {
					req.Headers[i].Value = val
					req.Headers[i].Enabled = true
					return goja.Undefined()
				}
			}
			req.Headers = append(req.Headers, BruHeader{Name: name, Value: val, Enabled: true})
		}
		return goja.Undefined()
	})
	reqObj.Set("getBody", func(call goja.FunctionCall) goja.Value {
		return sr.vm.ToValue(req.BodyContent)
	})
	reqObj.Set("setBody", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) > 0 {
			req.BodyContent = call.Arguments[0].String()
		}
		return goja.Undefined()
	})
	sr.vm.Set("req", reqObj)
}

func (sr *ScriptRunner) RunPreRequest(req *BrunoRequest) error {
	sr.bindReqObject(req)

	// Run collection pre-request first
	if sr.coll != nil && sr.coll.CollectionPreRequestScript != "" {
		if err := sr.run(sr.coll.CollectionPreRequestScript); err != nil {
			sr.log("error", fmt.Sprintf("collection script error: %v", err))
			return fmt.Errorf("collection pre-request script: %w", err)
		}
	}

	// Re-bind req in case sub-request replaced it during bru.runRequest
	sr.bindReqObject(req)

	// Run request pre-request
	if req.PreRequestScript != "" {
		if err := sr.run(req.PreRequestScript); err != nil {
			sr.log("error", fmt.Sprintf("request script error: %v", err))
			return fmt.Errorf("pre-request script: %w", err)
		}
	}

	return nil
}

func (sr *ScriptRunner) RunPostResponse(req *BrunoRequest, resp *HTTPResponse) error {
	var bodyVal any = resp.Body
	if resp.IsJSON {
		var parsed any
		if json.Unmarshal([]byte(resp.Body), &parsed) == nil {
			bodyVal = parsed
		}
	}

	resObj := sr.vm.NewObject()
	resObj.Set("getStatus", func(call goja.FunctionCall) goja.Value {
		return sr.vm.ToValue(resp.Status)
	})
	resObj.Set("status", resp.Status)
	resObj.Set("getBody", func(call goja.FunctionCall) goja.Value {
		return sr.vm.ToValue(bodyVal)
	})
	resObj.Set("body", bodyVal)
	resObj.Set("getHeaders", func(call goja.FunctionCall) goja.Value {
		return sr.vm.ToValue(resp.Headers)
	})
	resObj.Set("headers", resp.Headers)
	sr.vm.Set("res", resObj)

	if req.PostResponseScript != "" {
		sr.bindReqObject(req)
		if err := sr.run(req.PostResponseScript); err != nil {
			sr.log("error", fmt.Sprintf("post-response script error: %v", err))
			return fmt.Errorf("post-response script: %w", err)
		}
	}
	return nil
}

// formatArgs renders console.* arguments like Node does: strings verbatim,
// objects/arrays as indented JSON, errors as "Name: message", joined by spaces.
func (sr *ScriptRunner) formatArgs(args []goja.Value) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, sr.formatValue(a))
	}
	return strings.Join(parts, " ")
}

func (sr *ScriptRunner) formatValue(v goja.Value) string {
	switch {
	case v == nil || goja.IsUndefined(v):
		return "undefined"
	case goja.IsNull(v):
		return "null"
	}
	obj, ok := v.(*goja.Object)
	if !ok {
		return v.String()
	}
	switch obj.ClassName() {
	case "Error", "Function", "RegExp", "Date":
		return obj.String()
	}
	stringify, ok := goja.AssertFunction(sr.vm.Get("JSON").ToObject(sr.vm).Get("stringify"))
	if !ok {
		return obj.String()
	}
	out, err := stringify(goja.Undefined(), obj, goja.Undefined(), sr.vm.ToValue(2))
	if err != nil || goja.IsUndefined(out) { // e.g. circular structures
		return obj.String()
	}
	return out.String()
}
