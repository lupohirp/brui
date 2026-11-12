package bru

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dop251/goja"
)

var awaitKeywordRe = regexp.MustCompile(`\bawait\s+`)

type ScriptRunner struct {
	vm   *goja.Runtime
	coll *BrunoCollection
	logs []ConsoleLogEntry
}

func NewScriptRunner(coll *BrunoCollection) *ScriptRunner {
	sr := &ScriptRunner{
		vm:   goja.New(),
		coll: coll,
		logs: []ConsoleLogEntry{},
	}
	sr.initEnvironment()
	return sr
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
		sr.log("info", formatArgs(call.Arguments))
		return goja.Undefined()
	})
	consoleObj.Set("info", func(call goja.FunctionCall) goja.Value {
		sr.log("info", formatArgs(call.Arguments))
		return goja.Undefined()
	})
	consoleObj.Set("warn", func(call goja.FunctionCall) goja.Value {
		sr.log("warn", formatArgs(call.Arguments))
		return goja.Undefined()
	})
	consoleObj.Set("error", func(call goja.FunctionCall) goja.Value {
		sr.log("error", formatArgs(call.Arguments))
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
			k := call.Arguments[0].String()
			v := call.Arguments[1].String()
			if sr.coll.ActiveEnvironment != "" {
				for _, env := range sr.coll.Environments {
					if env.Name == sr.coll.ActiveEnvironment {
						env.Variables[k] = v
						break
					}
				}
			}
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
		_ = sr.RunPreRequest(subReq)
		subResp, err := ExecuteRequest(subReq, sr.coll)
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
		if _, err := sr.vm.RunString(wrapUserScript(sr.coll.CollectionPreRequestScript)); err != nil {
			sr.log("error", fmt.Sprintf("collection script error: %v", err))
		}
	}

	// Re-bind req in case sub-request replaced it during bru.runRequest
	sr.bindReqObject(req)

	// Run request pre-request
	if req.PreRequestScript != "" {
		if _, err := sr.vm.RunString(wrapUserScript(req.PreRequestScript)); err != nil {
			sr.log("error", fmt.Sprintf("request script error: %v", err))
			return err
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
		if _, err := sr.vm.RunString(wrapUserScript(req.PostResponseScript)); err != nil {
			sr.log("error", fmt.Sprintf("post-response script error: %v", err))
			return err
		}
	}
	return nil
}

func formatArgs(args []goja.Value) string {
	var parts []string
	for _, a := range args {
		parts = append(parts, a.String())
	}
	return fmt.Sprint(parts)
}
