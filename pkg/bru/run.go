package bru

import (
	"context"
	"fmt"
)

// RunOptions carries long-lived services used while running a request.
type RunOptions struct {
	// OAuth2 caches tokens across runs; nil uses a throwaway in-memory cache.
	OAuth2 *OAuth2Manager
	// Prompt asks the user to complete an authorization_code login; nil means
	// non-interactive (only loopback callback_url flows can succeed).
	Prompt PromptFunc
}

// RunResult is the outcome of RunWithScripts.
type RunResult struct {
	Response *HTTPResponse
	// Err is set when the request could not complete: pre-request script
	// failure, network error, timeout or cancellation.
	Err error
	// PostScriptErr is set when the response arrived but the post-response
	// script failed.
	PostScriptErr error
	Logs          []ConsoleLogEntry
	// RuntimeVariables holds the runtime variables after the scripts ran, to
	// be merged back into the live collection by the caller.
	RuntimeVariables map[string]any
	// AuthNote describes OAuth2 token handling ("new token", "token
	// refreshed", ...); empty when a cached token was reused or no OAuth2.
	AuthNote string
}

// RunWithScripts runs the full pipeline (collection + request pre-request
// scripts, HTTP call, post-response script) on the given request and
// collection. It is meant to run off the UI goroutine on private copies (see
// BrunoRequest.Clone and BrunoCollection.CloneForExecution); cancelling ctx
// aborts scripts and the HTTP call.
func RunWithScripts(ctx context.Context, req *BrunoRequest, coll *BrunoCollection, opts RunOptions) *RunResult {
	sr := NewScriptRunnerContext(ctx, coll)
	sr.opts = opts
	res := &RunResult{}
	defer func() {
		res.Logs = sr.GetLogs()
		res.RuntimeVariables = coll.RuntimeVariables
	}()

	if err := sr.RunPreRequest(req); err != nil {
		res.Err = err
		return res
	}
	// Like Bruno: the token is resolved after pre-request scripts (which may
	// set variables used by the OAuth2 config) and injected into the request,
	// so the post-response script sees it via req.getHeader("Authorization").
	note, err := applyOAuth2(ctx, req, coll, opts)
	res.AuthNote = note
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		res.Err = fmt.Errorf("oauth2: %w", err)
		return res
	}
	resp, err := ExecuteRequestContext(ctx, req, coll)
	if err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("request cancelled: %w", ctx.Err())
		}
		res.Err = err
		return res
	}
	res.Response = resp
	res.PostScriptErr = sr.RunPostResponse(req, resp)
	return res
}

// CloneForExecution returns a copy of the collection that a background
// request can read and mutate (runtime variables, request tree used by
// bru.runRequest) without racing with the UI.
func (c *BrunoCollection) CloneForExecution() *BrunoCollection {
	cp := *c
	cp.RuntimeVariables = make(map[string]any, len(c.RuntimeVariables))
	for k, v := range c.RuntimeVariables {
		cp.RuntimeVariables[k] = v
	}
	cp.CollectionHeaders = append([]BruHeader(nil), c.CollectionHeaders...)
	cp.Environments = make([]*BrunoEnvironment, len(c.Environments))
	for i, e := range c.Environments {
		ec := *e
		ec.SecretNames = append([]string(nil), e.SecretNames...)
		ec.Variables = make(map[string]string, len(e.Variables))
		for k, v := range e.Variables {
			ec.Variables[k] = v
		}
		cp.Environments[i] = &ec
	}
	cp.Requests = cloneRequests(c.Requests)
	cp.Folders = cloneFolders(c.Folders)
	return &cp
}

func cloneRequests(rs []*BrunoRequest) []*BrunoRequest {
	out := make([]*BrunoRequest, len(rs))
	for i, r := range rs {
		out[i] = r.Clone()
	}
	return out
}

func cloneFolders(fs []*BrunoFolder) []*BrunoFolder {
	out := make([]*BrunoFolder, len(fs))
	for i, f := range fs {
		fc := *f
		fc.Folders = cloneFolders(f.Folders)
		fc.Requests = cloneRequests(f.Requests)
		out[i] = &fc
	}
	return out
}
