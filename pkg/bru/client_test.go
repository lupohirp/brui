package bru

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDefaultClientVerifiesTLSAndHonoursProxy(t *testing.T) {
	tr := httpClient.Transport.(*http.Transport)
	if tr.TLSClientConfig != nil && tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("TLS verification must be enabled by default")
	}
	if tr.Proxy == nil {
		t.Error("transport must honour HTTP(S)_PROXY / NO_PROXY")
	}
}

func TestExecuteRequestTLSVerification(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.URL.RequestURI())
	}))
	defer srv.Close()
	t.Cleanup(func() { SetInsecureSkipVerify(false) })

	coll := &BrunoCollection{RuntimeVariables: map[string]any{"base": srv.URL}}
	req := &BrunoRequest{
		Method: MethodGet,
		URL:    "{{base}}/users/:id?a=1",
		Params: []BruParam{
			{Name: "a", Value: "1", Enabled: true},
			{Name: "b", Value: "x y", Enabled: true},
			{Name: "id", Value: "42", Enabled: true, Type: ParamTypePath},
		},
	}

	// Self-signed certificate: rejected by default, with an actionable hint.
	_, err := ExecuteRequest(req, coll)
	if err == nil || !strings.Contains(err.Error(), "--insecure") {
		t.Fatalf("expected TLS verification error with hint, got %v", err)
	}

	SetInsecureSkipVerify(true)
	resp, err := ExecuteRequest(req, coll)
	if err != nil {
		t.Fatalf("insecure mode should accept the certificate: %v", err)
	}
	if resp.Body != "/users/42?a=1&b=x+y" {
		t.Errorf("unexpected request URI: %s", resp.Body)
	}
}
