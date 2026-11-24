package bru

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cookieServer(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "s3cr3t-session", Path: "/", HttpOnly: true, MaxAge: 3600})
		http.SetCookie(w, &http.Cookie{Name: "pref", Value: "dark"}) // session cookie, default path "/"
	})
	mux.HandleFunc("/sso", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sso", Value: "from-redirect", Path: "/"})
		http.Redirect(w, r, "/me", http.StatusFound)
	})
	mux.HandleFunc("/me", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("Cookie"))
	})
	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", MaxAge: -1})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func useTestJar(t *testing.T, path string) *CookieJar {
	old := cookieJar
	t.Cleanup(func() { UseCookieJar(old) })
	return UseCookieJar(NewCookieJar(path))
}

func get(t *testing.T, u string) *HTTPResponse {
	t.Helper()
	res, err := ExecuteRequest(&BrunoRequest{Method: MethodGet, URL: u}, &BrunoCollection{})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestCookiesStoredAndSentAutomatically(t *testing.T) {
	fakeMachine(t)
	srv := cookieServer(t)
	path := filepath.Join(t.TempDir(), "cookies.json")
	jar := useTestJar(t, path)

	res := get(t, srv.URL+"/login")
	if len(res.Cookies) != 2 || res.Cookies[0].Name != "session" {
		t.Fatalf("response cookies: %+v", res.Cookies)
	}
	if body := get(t, srv.URL+"/me").Body; !strings.Contains(body, "session=s3cr3t-session") || !strings.Contains(body, "pref=dark") {
		t.Fatalf("cookies not sent back: %q", body)
	}

	// Cookies set by a response that redirects are kept too.
	if body := get(t, srv.URL+"/sso").Body; !strings.Contains(body, "sso=from-redirect") {
		t.Fatalf("redirect cookie not sent: %q", body)
	}
	if n := len(jar.List()); n != 3 {
		t.Fatalf("jar has %d cookies, want 3: %+v", n, jar.List())
	}

	// Persisted encrypted, private, and restored by a new jar (next start).
	data, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if strings.Contains(string(data), "s3cr3t-session") || info.Mode().Perm() != 0o600 {
		t.Fatalf("cookie file must be encrypted and 0600: %s %v", data, info.Mode())
	}
	u, _ := url.Parse(srv.URL + "/me")
	restored := NewCookieJar(path).Cookies(u)
	if len(restored) != 3 {
		t.Fatalf("restored cookies: %+v", restored)
	}

	// Max-Age=0 from the server deletes the cookie.
	get(t, srv.URL+"/logout")
	if body := get(t, srv.URL+"/me").Body; strings.Contains(body, "session=") {
		t.Fatalf("deleted cookie still sent: %q", body)
	}

	jar.Clear()
	if body := get(t, srv.URL+"/me").Body; body != "" || len(jar.List()) != 0 {
		t.Fatalf("cleared jar still sends %q", body)
	}
	if restored := NewCookieJar(path).Cookies(u); len(restored) != 0 {
		t.Fatal("clear must also empty the file")
	}
}

func TestExplicitCookieHeaderIsKept(t *testing.T) {
	srv := cookieServer(t)
	useTestJar(t, "")
	get(t, srv.URL+"/login")
	res, err := ExecuteRequest(&BrunoRequest{Method: MethodGet, URL: srv.URL + "/me",
		Headers: []BruHeader{{Name: "Cookie", Value: "manual=1", Enabled: true}}}, &BrunoCollection{})
	if err != nil || !strings.Contains(res.Body, "manual=1") || !strings.Contains(res.Body, "session=") {
		t.Fatalf("manual + jar cookies: %v %q", err, res.Body)
	}
}

func TestDefaultCookiePath(t *testing.T) {
	for in, want := range map[string]string{"": "/", "/": "/", "/a": "/", "/a/b": "/a", "/a/b/": "/a/b", "x": "/"} {
		if got := defaultCookiePath(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
