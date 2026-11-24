package bru

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// CookieJar stores the cookies servers set (Set-Cookie) and sends them back
// automatically, like Bruno. It wraps net/http/cookiejar (which implements
// the RFC 6265 matching rules) and persists cookies between sessions in a
// 0600 file with values encrypted like secrets.
type CookieJar struct {
	mu     sync.Mutex
	jar    *cookiejar.Jar
	stored map[string]*StoredCookie // domain|path|name
	path   string                   // "" = memory only
	now    func() time.Time
}

// StoredCookie is a cookie kept in the jar.
type StoredCookie struct {
	Name     string    `json:"name"`
	Value    string    `json:"value"` // encrypted on disk
	Domain   string    `json:"domain"`
	HostOnly bool      `json:"hostOnly,omitempty"`
	Path     string    `json:"path"`
	Expires  time.Time `json:"expires,omitzero"` // zero = session cookie
	Secure   bool      `json:"secure,omitempty"`
	HTTPOnly bool      `json:"httpOnly,omitempty"`
	SameSite string    `json:"sameSite,omitempty"`
}

// NewCookieJar returns a jar persisted to path ("" keeps it in memory).
func NewCookieJar(path string) *CookieJar {
	j := &CookieJar{path: path, now: time.Now}
	j.reset()
	j.load()
	return j
}

// DefaultCookieJarPath is the cookie file in the user cache directory.
func DefaultCookieJarPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "brui", "cookies.json")
}

func (j *CookieJar) reset() {
	j.jar, _ = cookiejar.New(nil)
	j.stored = map[string]*StoredCookie{}
}

func cookieKey(domain, path, name string) string { return domain + "|" + path + "|" + name }

// SetCookies implements http.CookieJar.
func (j *CookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if len(cookies) == 0 {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.jar.SetCookies(u, cookies)
	host := strings.ToLower(u.Hostname())
	now := j.now()
	for _, c := range cookies {
		sc := &StoredCookie{Name: c.Name, Value: c.Value, Path: c.Path, Secure: c.Secure, HTTPOnly: c.HttpOnly, SameSite: sameSiteName(c.SameSite)}
		if d := strings.TrimPrefix(strings.ToLower(c.Domain), "."); d != "" {
			if host != d && !strings.HasSuffix(host, "."+d) {
				continue // rejected by the jar as well
			}
			sc.Domain = d
		} else {
			sc.Domain, sc.HostOnly = host, true
		}
		if sc.Path == "" || !strings.HasPrefix(sc.Path, "/") {
			sc.Path = defaultCookiePath(u.Path)
		}
		switch {
		case c.MaxAge < 0:
			sc.Expires = now.Add(-time.Second)
		case c.MaxAge > 0:
			sc.Expires = now.Add(time.Duration(c.MaxAge) * time.Second)
		case !c.Expires.IsZero():
			sc.Expires = c.Expires
		}
		key := cookieKey(sc.Domain, sc.Path, sc.Name)
		if !sc.Expires.IsZero() && !sc.Expires.After(now) {
			delete(j.stored, key) // deletion request
			continue
		}
		j.stored[key] = sc
	}
	j.save()
}

// Cookies implements http.CookieJar.
func (j *CookieJar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.jar.Cookies(u)
}

// List returns the cookies in the jar (expired ones dropped), sorted.
func (j *CookieJar) List() []StoredCookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := j.now()
	var out []StoredCookie
	for k, c := range j.stored {
		if !c.Expires.IsZero() && !c.Expires.After(now) {
			delete(j.stored, k)
			continue
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Domain != out[b].Domain {
			return out[a].Domain < out[b].Domain
		}
		if out[a].Path != out[b].Path {
			return out[a].Path < out[b].Path
		}
		return out[a].Name < out[b].Name
	})
	return out
}

// Clear removes every cookie.
func (j *CookieJar) Clear() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.reset()
	j.save()
}

type cookieFile struct {
	Cookies []StoredCookie `json:"cookies"`
}

func (j *CookieJar) load() {
	if j.path == "" {
		return
	}
	data, err := os.ReadFile(j.path)
	if err != nil {
		return
	}
	var f cookieFile
	if json.Unmarshal(data, &f) != nil {
		return
	}
	now := j.now()
	store := &SecretStore{}
	for _, c := range f.Cookies {
		if !c.Expires.IsZero() && !c.Expires.After(now) {
			continue
		}
		val, err := store.decrypt(c.Value)
		if err != nil {
			continue // written on another machine
		}
		c.Value = val
		scheme := "http"
		if c.Secure {
			scheme = "https"
		}
		hc := &http.Cookie{Name: c.Name, Value: c.Value, Path: c.Path, Expires: c.Expires, Secure: c.Secure, HttpOnly: c.HTTPOnly}
		if !c.HostOnly {
			hc.Domain = c.Domain
		}
		j.jar.SetCookies(&url.URL{Scheme: scheme, Host: c.Domain, Path: c.Path}, []*http.Cookie{hc})
		cc := c
		j.stored[cookieKey(c.Domain, c.Path, c.Name)] = &cc
	}
}

// save writes the jar; called with j.mu held. Failures are ignored: cookies
// then simply last for this session.
func (j *CookieJar) save() {
	if j.path == "" {
		return
	}
	f := cookieFile{Cookies: []StoredCookie{}}
	for _, c := range j.stored {
		enc, err := encryptSecret(c.Value)
		if err != nil {
			return // no machine key: never write cookie values in clear
		}
		cc := *c
		cc.Value = enc
		f.Cookies = append(f.Cookies, cc)
	}
	sort.Slice(f.Cookies, func(a, b int) bool {
		return cookieKey(f.Cookies[a].Domain, f.Cookies[a].Path, f.Cookies[a].Name) <
			cookieKey(f.Cookies[b].Domain, f.Cookies[b].Path, f.Cookies[b].Name)
	})
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(j.path), 0o700) != nil {
		return
	}
	tmp := j.path + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, j.path)
	}
}

// defaultCookiePath implements RFC 6265 section 5.1.4.
func defaultCookiePath(p string) string {
	if p == "" || p[0] != '/' {
		return "/"
	}
	i := strings.LastIndex(p, "/")
	if i == 0 {
		return "/"
	}
	return p[:i]
}

func sameSiteName(s http.SameSite) string {
	switch s {
	case http.SameSiteLaxMode:
		return "Lax"
	case http.SameSiteStrictMode:
		return "Strict"
	case http.SameSiteNoneMode:
		return "None"
	}
	return ""
}

// cookieJar is the jar used by every request; replaced at startup with a
// persistent one (UseCookieJar).
var cookieJar = NewCookieJar("")

// UseCookieJar installs the jar used for all requests and returns it.
func UseCookieJar(j *CookieJar) *CookieJar {
	cookieJar = j
	httpClient.Jar = j
	return j
}

// ActiveCookieJar returns the jar used for requests.
func ActiveCookieJar() *CookieJar { return cookieJar }
