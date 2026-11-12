package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lupohirp/brui/pkg/bru"
	"github.com/lupohirp/brui/pkg/theme"
)

type TreeItem struct {
	Type     string // "folder" or "request"
	Folder   *bru.BrunoFolder
	Request  *bru.BrunoRequest
	Depth    int
	Expanded bool
}

type Model struct {
	Coll            *bru.BrunoCollection
	Theme           theme.Theme
	Width           int
	Height          int
	SelectedIdx     int
	VisibleItems    []TreeItem
	SelectedRequest *bru.BrunoRequest
	ActiveTab       string // "headers", "params", "body", "auth", "docs"
	ActiveRespTab   string // "body", "headers", "console"
	LastResponse    *bru.HTTPResponse
	ScriptRunner    *bru.ScriptRunner
	StatusMsg       string

	// Inline Edit
	IsEditingURL bool
	URLEditText  string
	URLCursor    int

	// Viewports
	ReqViewport  viewport.Model
	RespViewport viewport.Model
}

func NewModel(coll *bru.BrunoCollection) Model {
	m := Model{
		Coll:          coll,
		Theme:         theme.Mocha,
		ActiveTab:     "headers",
		ActiveRespTab: "body",
		ScriptRunner:  bru.NewScriptRunner(coll),
		StatusMsg:     "Ready",
	}
	m.rebuildTree()
	if len(m.VisibleItems) > 0 {
		for _, it := range m.VisibleItems {
			if it.Type == "request" {
				m.SelectedRequest = it.Request
				break
			}
		}
	}
	return m
}

func (m *Model) rebuildTree() {
	var items []TreeItem
	for _, req := range m.Coll.Requests {
		items = append(items, TreeItem{Type: "request", Request: req, Depth: 0})
	}

	var walkFolders func(f *bru.BrunoFolder, depth int)
	walkFolders = func(f *bru.BrunoFolder, depth int) {
		items = append(items, TreeItem{Type: "folder", Folder: f, Depth: depth, Expanded: true})
		for _, subF := range f.Folders {
			walkFolders(subF, depth+1)
		}
		for _, req := range f.Requests {
			items = append(items, TreeItem{Type: "request", Request: req, Depth: depth + 1})
		}
	}

	for _, f := range m.Coll.Folders {
		walkFolders(f, 0)
	}

	m.VisibleItems = items
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
		sidebarW := m.Width * 32 / 100
		contentW := m.Width - sidebarW - 4
		paneH := (m.Height - 8) / 2

		m.ReqViewport = viewport.New(contentW, paneH)
		m.RespViewport = viewport.New(contentW, paneH)
		return m, nil

	case tea.KeyMsg:
		if m.IsEditingURL {
			switch msg.Type {
			case tea.KeyEsc:
				m.IsEditingURL = false
				return m, nil
			case tea.KeyEnter:
				m.IsEditingURL = false
				if m.SelectedRequest != nil {
					m.SelectedRequest.URL = m.URLEditText
					_ = osWriteRequest(m.SelectedRequest)
				}
				return m, nil
			case tea.KeyBackspace:
				if m.URLCursor > 0 && len(m.URLEditText) > 0 {
					m.URLEditText = m.URLEditText[:m.URLCursor-1] + m.URLEditText[m.URLCursor:]
					m.URLCursor--
				}
				return m, nil
			case tea.KeyLeft:
				if m.URLCursor > 0 {
					m.URLCursor--
				}
				return m, nil
			case tea.KeyRight:
				if m.URLCursor < len(m.URLEditText) {
					m.URLCursor++
				}
				return m, nil
			case tea.KeyRunes:
				ch := string(msg.Runes)
				m.URLEditText = m.URLEditText[:m.URLCursor] + ch + m.URLEditText[m.URLCursor:]
				m.URLCursor += len(ch)
				return m, nil
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "up", "k":
			if m.SelectedIdx > 0 {
				m.SelectedIdx--
				m.selectCurrentItem()
			}
		case "down", "j":
			if m.SelectedIdx < len(m.VisibleItems)-1 {
				m.SelectedIdx++
				m.selectCurrentItem()
			}

		case "u", "U":
			if m.SelectedRequest != nil {
				m.IsEditingURL = true
				m.URLEditText = m.SelectedRequest.URL
				m.URLCursor = len(m.URLEditText)
			}

		case "1":
			m.ActiveTab = "headers"
		case "2":
			m.ActiveTab = "params"
		case "3":
			m.ActiveTab = "body"
		case "4":
			m.ActiveTab = "auth"
		case "5":
			m.ActiveTab = "docs"

		case " ", "enter":
			if m.SelectedRequest != nil {
				m.StatusMsg = fmt.Sprintf("Sending %s %s...", m.SelectedRequest.Method, m.SelectedRequest.Name)
				_ = m.ScriptRunner.RunPreRequest(m.SelectedRequest)
				resp, err := bru.ExecuteRequest(m.SelectedRequest, m.Coll)
				if err != nil {
					m.StatusMsg = fmt.Sprintf("Error: %v", err)
				} else {
					m.LastResponse = resp
					_ = m.ScriptRunner.RunPostResponse(m.SelectedRequest, resp)
					m.StatusMsg = fmt.Sprintf("Done: %d %s (%dms)", resp.Status, resp.StatusText, resp.DurationMs)
				}
			}
		}

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			// Click on sidebar
			sidebarW := m.Width * 32 / 100
			if msg.X < sidebarW && msg.Y >= 4 && msg.Y < m.Height-2 {
				row := msg.Y - 4
				if row >= 0 && row < len(m.VisibleItems) {
					m.SelectedIdx = row
					m.selectCurrentItem()
				}
			}
		}
	}

	return m, nil
}

func (m *Model) selectCurrentItem() {
	if m.SelectedIdx >= 0 && m.SelectedIdx < len(m.VisibleItems) {
		it := m.VisibleItems[m.SelectedIdx]
		if it.Type == "request" {
			m.SelectedRequest = it.Request
		}
	}
}

func osWriteRequest(req *bru.BrunoRequest) error {
	raw := bru.SerializeRequest(req)
	return osWriteFile(req.FilePath, raw)
}

func osWriteFile(path, content string) error {
	return nil // placeholder
}

func (m Model) View() string {
	if m.Width == 0 || m.Height == 0 {
		return "Loading brui..."
	}

	// 1. Header (Dog pixel art + Collection/Env)
	header := m.renderHeader()

	// 2. Body (Left sidebar + Right Request/Response)
	sidebarW := m.Width * 32 / 100
	contentW := m.Width - sidebarW - 3
	contentH := m.Height - 7

	sidebar := m.renderSidebar(sidebarW, contentH)
	content := m.renderContent(contentW, contentH)

	body := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, content)

	// 3. Status Bar
	statusBar := m.renderStatusBar()

	return lipgloss.JoinVertical(lipgloss.Left, header, body, statusBar)
}

func (m Model) renderHeader() string {
	E := "#c97a2b"
	Y := "#f4aa42"
	K := "#11111b"
	R := "#ea5a47"

	eStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(E))
	yStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(Y))
	kStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(K))
	rStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(R))
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Accent)).Bold(true)
	subtext := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Subtext))
	putStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Put)).Bold(true)

	dog1 := eStyle.Render("▄█") + yStyle.Render("█████") + eStyle.Render("█▄")
	dog2 := eStyle.Render("▀") + kStyle.Render("▀") + yStyle.Render("█") + kStyle.Render("▀") + eStyle.Render("▀")
	dog3 := yStyle.Render(" ▀▀") + rStyle.Render("█") + yStyle.Render("▀▀ ")

	envName := m.Coll.ActiveEnvironment
	if envName == "" {
		envName = "None"
	}

	l1 := fmt.Sprintf(" %s  %s %s", dog1, accent.Render("BRUI"), subtext.Render("- Bruno Terminal Client (Go Native)"))
	l2 := fmt.Sprintf(" %s  %s %s   %s %s", dog2, subtext.Render("Collection [C]:"), putStyle.Render("["+m.Coll.Name+"]"), subtext.Render("Env [E]:"), putStyle.Render("["+envName+"]"))
	l3 := fmt.Sprintf(" %s", dog3)

	boxStyle := lipgloss.NewStyle().
		Width(m.Width).
		Height(4).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.Theme.Border))

	return boxStyle.Render(strings.Join([]string{l1, l2, l3}, "\n"))
}

func (m Model) renderSidebar(width, height int) string {
	var lines []string
	subtext := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Subtext))

	for i, it := range m.VisibleItems {
		indent := strings.Repeat("  ", it.Depth)
		if it.Type == "folder" {
			marker := "v"
			total := len(it.Folder.Requests) + len(it.Folder.Folders)
			fStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Accent)).Bold(true)
			lines = append(lines, fmt.Sprintf("%s%s [%s] %s", indent, fStyle.Render(marker), it.Folder.Name, subtext.Render(fmt.Sprintf("(%d)", total))))
		} else {
			req := it.Request
			badgeColor := theme.MethodColor(string(req.Method), m.Theme)
			badgeStyle := lipgloss.NewStyle().Foreground(badgeColor).Bold(true)
			nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Fg))
			if i == m.SelectedIdx {
				nameStyle = nameStyle.Background(lipgloss.Color(m.Theme.Selection)).Bold(true)
			}
			badge := badgeStyle.Render(fmt.Sprintf("%-5s", req.Method))
			lines = append(lines, fmt.Sprintf("%s %s %s", indent, badge, nameStyle.Render(req.Name)))
		}
	}

	style := lipgloss.NewStyle().
		Width(width).
		Height(height).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.Theme.BorderFocus))

	return style.Render(strings.Join(lines, "\n"))
}

func (m Model) renderContent(width, height int) string {
	if m.SelectedRequest == nil {
		return lipgloss.NewStyle().Width(width).Height(height).Render("No request selected")
	}

	req := m.SelectedRequest
	badgeColor := theme.MethodColor(string(req.Method), m.Theme)
	badge := lipgloss.NewStyle().Foreground(badgeColor).Bold(true).Render(fmt.Sprintf("[ %s ]", req.Method))

	urlStr := req.URL
	if m.IsEditingURL {
		curChar := " "
		if m.URLCursor < len(m.URLEditText) {
			curChar = string(m.URLEditText[m.URLCursor])
		}
		curBlock := lipgloss.NewStyle().
			Foreground(lipgloss.Color(m.Theme.SelectionFg)).
			Background(lipgloss.Color(m.Theme.Accent)).
			Bold(true).
			Render(curChar)

		before := m.URLEditText[:m.URLCursor]
		after := ""
		if m.URLCursor < len(m.URLEditText) {
			after = m.URLEditText[m.URLCursor+1:]
		}
		urlStr = before + curBlock + after
	}

	headerLine := fmt.Sprintf(" %s  %s", badge, urlStr)

	tabs := []string{"Headers", "Params", "Body", "Auth", "Docs"}
	var tabStrs []string
	for _, t := range tabs {
		tl := strings.ToLower(t)
		if tl == m.ActiveTab {
			tabStrs = append(tabStrs, lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Accent)).Bold(true).Render(fmt.Sprintf("[ %s ]", t)))
		} else {
			tabStrs = append(tabStrs, lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Subtext)).Render(fmt.Sprintf("  %s  ", t)))
		}
	}
	tabsLine := strings.Join(tabStrs, " ")

	paneH := height / 2
	reqBoxStyle := lipgloss.NewStyle().
		Width(width).
		Height(paneH).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.Theme.Border))

	reqBody := ""
	if m.ActiveTab == "headers" {
		var hLines []string
		for _, h := range req.Headers {
			hLines = append(hLines, fmt.Sprintf(" %s: %s", h.Name, h.Value))
		}
		if len(m.Coll.CollectionHeaders) > 0 {
			hLines = append(hLines, fmt.Sprintf("\n --- Inherited Headers (%d) ---", len(m.Coll.CollectionHeaders)))
			for _, ch := range m.Coll.CollectionHeaders {
				hLines = append(hLines, fmt.Sprintf(" %s: %s", ch.Name, ch.Value))
			}
		}
		reqBody = strings.Join(hLines, "\n")
	} else if m.ActiveTab == "body" {
		reqBody = req.BodyContent
	}

	reqPane := reqBoxStyle.Render(fmt.Sprintf("%s\n%s\n\n%s", headerLine, tabsLine, reqBody))

	// Response Pane
	respBoxStyle := lipgloss.NewStyle().
		Width(width).
		Height(paneH).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.Theme.Border))

	respContent := " No response yet. Press [Space] or [Enter] to send."
	if m.LastResponse != nil {
		r := m.LastResponse
		statusColor := m.Theme.Success
		if r.Status >= 400 {
			statusColor = m.Theme.Error
		}
		stBadge := lipgloss.NewStyle().Foreground(lipgloss.Color(statusColor)).Bold(true).Render(fmt.Sprintf("%d %s", r.Status, r.StatusText))
		respContent = fmt.Sprintf(" %s  Time: %d ms  Size: %d B\n\n%s", stBadge, r.DurationMs, r.SizeBytes, r.Body)
	}

	respPane := respBoxStyle.Render(respContent)

	return lipgloss.JoinVertical(lipgloss.Left, reqPane, respPane)
}

func (m Model) renderStatusBar() string {
	shortcuts := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.Theme.Fg)).
		Render(" [Space] Send   [U] Edit URL   [1-5] Tabs   [q] Quit")

	status := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.Theme.Accent)).
		Bold(true).
		Render(m.StatusMsg)

	bar := fmt.Sprintf(" %s  │  %s", shortcuts, status)

	return lipgloss.NewStyle().
		Width(m.Width).
		Height(1).
		Background(lipgloss.Color(m.Theme.Surface)).
		Render(bar)
}
