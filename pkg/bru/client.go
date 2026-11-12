package bru

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

var httpClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
}

func ExecuteRequest(req *BrunoRequest, coll *BrunoCollection) (*HTTPResponse, error) {
	// 1. Interpolate URL
	finalURL := Interpolate(req.URL, coll)

	// 2. Body
	var bodyReader io.Reader
	if req.BodyType != "" && req.BodyType != "none" && req.BodyContent != "" {
		interpolatedBody := Interpolate(req.BodyContent, coll)
		bodyReader = strings.NewReader(interpolatedBody)
	}

	httpReq, err := http.NewRequest(string(req.Method), finalURL, bodyReader)
	if err != nil {
		return nil, err
	}

	// 3. Collection Headers first, then Request Headers
	headersMap := make(map[string]string)
	for _, ch := range coll.CollectionHeaders {
		if ch.Enabled && ch.Name != "" {
			headersMap[strings.ToLower(ch.Name)] = Interpolate(ch.Value, coll)
			httpReq.Header.Set(ch.Name, Interpolate(ch.Value, coll))
		}
	}
	for _, rh := range req.Headers {
		if rh.Enabled && rh.Name != "" {
			headersMap[strings.ToLower(rh.Name)] = Interpolate(rh.Value, coll)
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
