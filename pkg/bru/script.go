package bru

import (
	"fmt"
	"time"

	"github.com/dop251/goja"
)

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

func (sr *ScriptRunner) initEnvironment() {
	// console object
	consoleObj := sr.vm.NewObject()
	consoleObj.Set("log", func(call goja.FunctionCall) goja.Value {
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
	sr.vm.Set("bru", bruObj)
}

func (sr *ScriptRunner) RunPreRequest(req *BrunoRequest) error {
	// req object
	reqObj := sr.vm.NewObject()
	reqObj.Set("getUrl", func(call goja.FunctionCall) goja.Value {
		return sr.vm.ToValue(req.URL)
	})
	reqObj.Set("getMethod", func(call goja.FunctionCall) goja.Value {
		return sr.vm.ToValue(string(req.Method))
	})
	sr.vm.Set("req", reqObj)

	// Run collection pre-request first
	if sr.coll.CollectionPreRequestScript != "" {
		if _, err := sr.vm.RunString(sr.coll.CollectionPreRequestScript); err != nil {
			sr.log("error", fmt.Sprintf("collection script error: %v", err))
		}
	}

	// Run request pre-request
	if req.PreRequestScript != "" {
		if _, err := sr.vm.RunString(req.PreRequestScript); err != nil {
			sr.log("error", fmt.Sprintf("request script error: %v", err))
			return err
		}
	}

	return nil
}

func (sr *ScriptRunner) RunPostResponse(req *BrunoRequest, resp *HTTPResponse) error {
	resObj := sr.vm.NewObject()
	resObj.Set("getStatus", func(call goja.FunctionCall) goja.Value {
		return sr.vm.ToValue(resp.Status)
	})
	resObj.Set("getBody", func(call goja.FunctionCall) goja.Value {
		return sr.vm.ToValue(resp.Body)
	})
	sr.vm.Set("res", resObj)

	if req.PostResponseScript != "" {
		if _, err := sr.vm.RunString(req.PostResponseScript); err != nil {
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
