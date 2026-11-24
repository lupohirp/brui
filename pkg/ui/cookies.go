package ui

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lupohirp/brui/pkg/bru"
)

// cookieLines renders the Cookies tab: what the last response set, and the
// whole jar (sent automatically to matching requests).
func (m Model) cookieLines(width int) []string {
	t := m.Theme
	head := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent)).Bold(true)
	sub := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Subtext))
	name := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Patch))
	dom := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Put)).Bold(true)
	now := time.Now()
	valueW := max(12, width/3)
	short := func(v string) string { return ansi.Truncate(v, valueW, "…") }

	var lines []string
	if r := m.LastResponse; r != nil {
		lines = append(lines, head.Render(fmt.Sprintf(" Set by the last response (%d)", len(r.Cookies))))
		if len(r.Cookies) == 0 {
			lines = append(lines, sub.Render("   none"))
		}
		for _, c := range r.Cookies {
			lines = append(lines, "   "+name.Render(c.Name)+" = "+short(c.Value)+"  "+sub.Render(setCookieAttrs(c, now)))
		}
		lines = append(lines, "")
	}

	jar := bru.ActiveCookieJar().List()
	lines = append(lines, head.Render(fmt.Sprintf(" Cookie jar (%d)", len(jar)))+sub.Render("  sent automatically to matching requests · [Ctrl+L] delete all"))
	if len(jar) == 0 {
		lines = append(lines, sub.Render("   empty: cookies from Set-Cookie responses are stored here and kept between sessions"))
	}
	last := ""
	for _, c := range jar {
		if c.Domain != last {
			label := c.Domain
			if !c.HostOnly {
				label = "." + c.Domain + "  (and subdomains)"
			}
			lines = append(lines, "  "+dom.Render(label))
			last = c.Domain
		}
		lines = append(lines, "    "+name.Render(c.Name)+" = "+short(c.Value)+"  "+sub.Render(storedAttrs(c, now)))
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "…")
	}
	return lines
}

func expiryLabel(exp time.Time, now time.Time) string {
	if exp.IsZero() {
		return "session"
	}
	if d := exp.Sub(now); d > 0 {
		if d < 48*time.Hour {
			return "expires in " + d.Round(time.Minute).String()
		}
		return "expires " + exp.Local().Format("2006-01-02")
	}
	return "expired"
}

func storedAttrs(c bru.StoredCookie, now time.Time) string {
	attrs := []string{"path " + c.Path, expiryLabel(c.Expires, now)}
	if c.Secure {
		attrs = append(attrs, "Secure")
	}
	if c.HTTPOnly {
		attrs = append(attrs, "HttpOnly")
	}
	if c.SameSite != "" {
		attrs = append(attrs, "SameSite="+c.SameSite)
	}
	return strings.Join(attrs, " · ")
}

func setCookieAttrs(c *http.Cookie, now time.Time) string {
	exp := c.Expires
	switch {
	case c.MaxAge < 0:
		return "deleted by the server"
	case c.MaxAge > 0:
		exp = now.Add(time.Duration(c.MaxAge) * time.Second)
	}
	var attrs []string
	if c.Domain != "" {
		attrs = append(attrs, "domain "+c.Domain)
	}
	if c.Path != "" {
		attrs = append(attrs, "path "+c.Path)
	}
	attrs = append(attrs, expiryLabel(exp, now))
	if c.Secure {
		attrs = append(attrs, "Secure")
	}
	if c.HttpOnly {
		attrs = append(attrs, "HttpOnly")
	}
	return strings.Join(attrs, " · ")
}
