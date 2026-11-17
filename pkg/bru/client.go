package bru

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var httpClient = newHTTPClient(false)

// newHTTPClient builds the client used for all requests. It starts from Go's
// default transport so HTTP(S)_PROXY / NO_PROXY, HTTP/2 and sane dial/TLS
// timeouts are honoured. Certificate verification is on unless insecure.
func newHTTPClient(insecure bool) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: t}
}

// SetInsecureSkipVerify disables (or re-enables) TLS certificate verification.
// It must be called at startup, before any request is sent.
func SetInsecureSkipVerify(insecure bool) {
	httpClient = newHTTPClient(insecure)
}

func ExecuteRequest(req *BrunoRequest, coll *BrunoCollection) (*HTTPResponse, error) {
	// 1. Interpolate URL, substitute path params, append query params
	finalURL := BuildURL(req, coll)

	// 2. Body
	var bodyReader io.Reader
	if req.BodyType != "" && req.BodyType != "none" && req.BodyContent != "" {
		if req.BodyType == "form-urlencoded" || req.BodyType == "formUrlEncoded" {
			vals := url.Values{}
			for _, line := range strings.Split(req.BodyContent, "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "~") {
					continue
				}
				var k, v string
				if idx := strings.Index(trimmed, ":"); idx != -1 {
					k = strings.TrimSpace(trimmed[:idx])
					v = strings.TrimSpace(trimmed[idx+1:])
				} else if idx := strings.Index(trimmed, "="); idx != -1 {
					k = strings.TrimSpace(trimmed[:idx])
					v = strings.TrimSpace(trimmed[idx+1:])
				}
				if k != "" {
					vals.Set(Interpolate(k, coll), Interpolate(v, coll))
				}
			}
			bodyReader = strings.NewReader(vals.Encode())
		} else {
			interpolatedBody := Interpolate(req.BodyContent, coll)
			bodyReader = strings.NewReader(interpolatedBody)
		}
	}

	httpReq, err := http.NewRequest(string(req.Method), finalURL, bodyReader)
	if err != nil {
		return nil, err
	}

	// 3. Collection Headers first, then Request Headers
	for _, ch := range coll.CollectionHeaders {
		if ch.Enabled && ch.Name != "" {
			httpReq.Header.Set(ch.Name, Interpolate(ch.Value, coll))
		}
	}
	for _, rh := range req.Headers {
		if rh.Enabled && rh.Name != "" {
			httpReq.Header.Set(rh.Name, Interpolate(rh.Value, coll))
		}
	}

	// Default Content-Type for JSON
	if req.BodyType == "json" && httpReq.Header.Get("Content-Type") == "" {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	// 4. Auth
	effectiveAuth := req.AuthType
	if effectiveAuth == "inherit" {
		effectiveAuth = coll.CollectionAuthType
	}

	if effectiveAuth == "bearer" {
		tok := req.AuthBearerToken
		if tok == "" {
			tok = coll.CollectionAuthBearerToken
		}
		if tok == "" {
			tok = "{{access_token}}"
		}
		resTok := Interpolate(tok, coll)
		if resTok != "" {
			httpReq.Header.Set("Authorization", "Bearer "+resTok)
		}
	} else if effectiveAuth == "basic" {
		user := Interpolate(req.AuthBasicUser, coll)
		pass := Interpolate(req.AuthBasicPassword, coll)
		httpReq.SetBasicAuth(user, pass)
	}

	// 5. Send request
	start := time.Now()
	resp, err := httpClient.Do(httpReq)
	duration := time.Since(start).Milliseconds()
	if err != nil {
		var certErr *tls.CertificateVerificationError
		if errors.As(err, &certErr) {
			return nil, fmt.Errorf("%w (TLS verification failed: trust the CA via SSL_CERT_FILE, or run brui --insecure to skip verification)", err)
		}
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	respHeaders := make(map[string]string)
	for k, v := range resp.Header {
		respHeaders[k] = strings.Join(v, ", ")
	}

	rawBody := string(respBytes)
	isJSON := false
	var js any
	if json.Unmarshal(respBytes, &js) == nil {
		isJSON = true
	}

	return &HTTPResponse{
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		DurationMs: duration,
		SizeBytes:  int64(len(respBytes)),
		Headers:    respHeaders,
		Body:       rawBody,
		IsJSON:     isJSON,
	}, nil
}

// BuildURL returns the final request URL: variables are interpolated, `:name`
// path segments are replaced by enabled path params, and enabled query params
// are appended unless the URL already carries them (Bruno keeps the query
// string in the URL and mirrors it in `params:query`).
func BuildURL(req *BrunoRequest, coll *BrunoCollection) string {
	rawURL := strings.TrimSpace(req.URL)

	base, query, hasQuery := strings.Cut(rawURL, "?")
	present := map[string]bool{}
	if hasQuery {
		for _, part := range strings.Split(query, "&") {
			k, _, _ := strings.Cut(part, "=")
			present[k] = true
		}
	}

	pathVals := map[string]string{}
	for _, p := range req.Params {
		if p.Enabled && p.Type == ParamTypePath && p.Name != "" {
			pathVals[p.Name] = p.Value
		}
	}
	if len(pathVals) > 0 {
		segs := strings.Split(base, "/")
		for i, seg := range segs {
			if v, ok := pathVals[strings.TrimPrefix(seg, ":")]; ok && strings.HasPrefix(seg, ":") {
				segs[i] = url.PathEscape(Interpolate(v, coll))
			}
		}
		base = strings.Join(segs, "/")
	}

	finalURL := Interpolate(base, coll)
	if hasQuery {
		finalURL += "?" + Interpolate(query, coll)
	}

	var extra []string
	for _, p := range req.Params {
		if p.Enabled && p.Type != ParamTypePath && p.Name != "" && !present[p.Name] {
			extra = append(extra, url.QueryEscape(Interpolate(p.Name, coll))+"="+url.QueryEscape(Interpolate(p.Value, coll)))
		}
	}
	if len(extra) > 0 {
		sep := "?"
		if hasQuery {
			sep = "&"
		}
		finalURL += sep + strings.Join(extra, "&")
	}
	return finalURL
}

// SyncParamsFromURL rebuilds the enabled query params from the URL query
// string, as Bruno does when the URL is edited. Disabled query params and path
// params are kept.
func SyncParamsFromURL(req *BrunoRequest) {
	var params []BruParam
	for _, p := range req.Params {
		if p.Type == ParamTypePath || !p.Enabled {
			params = append(params, p)
		}
	}
	if _, query, ok := strings.Cut(req.URL, "?"); ok && query != "" {
		for _, part := range strings.Split(query, "&") {
			if part == "" {
				continue
			}
			k, v, _ := strings.Cut(part, "=")
			params = append(params, BruParam{Name: k, Value: v, Enabled: true})
		}
	}
	if params == nil {
		params = []BruParam{}
	}
	req.Params = params
}

// SyncURLFromParams rewrites the URL query string from the enabled query
// params, as Bruno does when the params table is edited.
func SyncURLFromParams(req *BrunoRequest) {
	base, _, _ := strings.Cut(req.URL, "?")
	var parts []string
	for _, p := range req.Params {
		if p.Enabled && p.Type != ParamTypePath && p.Name != "" {
			parts = append(parts, p.Name+"="+p.Value)
		}
	}
	if len(parts) > 0 {
		base += "?" + strings.Join(parts, "&")
	}
	req.URL = base
}
