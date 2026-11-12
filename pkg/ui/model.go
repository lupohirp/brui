package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lupohirp/brui/pkg/bru"
	"github.com/lupohirp/brui/pkg/theme"
)

var varRegex = regexp.MustCompile(`\{\{([a-zA-Z0-9_$-]+)\}\}`)

type TreeItem struct {
	Type     string // "folder" or "request"
	Folder   *bru.BrunoFolder
	Request  *bru.BrunoRequest
	Depth    int
	Expanded bool
}

type AutocompleteCandidate struct {
	Name string
	Type string // "dynamic", "env", "runtime", "system"
	Val  string
}

type Model struct {
	Coll            *bru.BrunoCollection
	Theme           theme.Theme
	Width           int
	Height          int
	SelectedIdx     int
	SidebarOffset   int
	VisibleItems    []TreeItem
	ExpandedFolders map[string]bool
	SelectedRequest *bru.BrunoRequest
	ActiveTab       string // "headers", "params", "body", "auth", "docs"
	ActiveRespTab   string // "body", "headers"
	LastResponse    *bru.HTTPResponse
	ScriptRunner    *bru.ScriptRunner
	StatusMsg       string
	ModifiedRequests map[string]bool

	// Inline Edit
	IsEditingURL bool
	URLEditText  string
	URLCursor    int

	// Autocomplete state
	ACCandidates  []AutocompleteCandidate
	ACSelectedIdx int
	ACMatchPrefix string
	ACMatchStart  int

	// Scrolling
	RespScroll int
	ReqScroll  int
}

func NewModel(coll *bru.BrunoCollection) Model {
	m := Model{
		Coll:             coll,
		Theme:            theme.Mocha,
		ActiveTab:        "headers",
		ActiveRespTab:    "body",
		ScriptRunner:     bru.NewScriptRunner(coll),
		StatusMsg:        "Ready",
		ExpandedFolders:  make(map[string]bool),
		ModifiedRequests: make(map[string]bool),
	}

	// Expand all folders by default
	var markExpanded func(f *bru.BrunoFolder)
	markExpanded = func(f *bru.BrunoFolder) {
		m.ExpandedFolders[f.ID] = true
		for _, sub := range f.Folders {
			markExpanded(sub)
		}
	}
	for _, f := range coll.Folders {
		markExpanded(f)
	}

	m.rebuildTree()
	if len(m.VisibleItems) > 0 {
		for i, it := range m.VisibleItems {
			if it.Type == "request" {
				m.SelectedIdx = i
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
		expanded := m.ExpandedFolders[f.ID]
		items = append(items, TreeItem{Type: "folder", Folder: f, Depth: depth, Expanded: expanded})
		if expanded {
			for _, subF := range f.Folders {
				walkFolders(subF, depth+1)
			}
			for _, req := range f.Requests {
				items = append(items, TreeItem{Type: "request", Request: req, Depth: depth + 1})
			}
		}
	}

	for _, f := range m.Coll.Folders {
		walkFolders(f, 0)
	}

	m.VisibleItems = items
	if m.SelectedIdx >= len(m.VisibleItems) {
		m.SelectedIdx = len(m.VisibleItems) - 1
	}
	if m.SelectedIdx < 0 {
		m.SelectedIdx = 0
	}
	m.selectCurrentItem()
}

func (m Model) layoutDimensions() (sidebarW, contentW, bodyH, reqH, respH int) {
	W := m.Width
	H := m.Height

	headerH := 5
	statusH := 1
	bodyH = H - headerH - statusH
	if bodyH < 4 {
		bodyH = 4
	}

	sidebarW = W * 30 / 100
	if sidebarW < 24 {
		sidebarW = 24
	}
	if sidebarW > 45 {
		sidebarW = 45
	}
	contentW = W - sidebarW
	if contentW < 20 {
		contentW = 20
	}

	reqH = bodyH * 52 / 100
	respH = bodyH - reqH
	return
}

func (m Model) Init() tea.Cmd {
	return tea.SetWindowTitle("brui")
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.IsEditingURL {
			return m.handleURLEditKeys(msg)
		}
		return m.handleNormalKeys(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)
	}

	return m, nil
}

func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.Width <= 0 || m.Height <= 0 {
		return *m, nil
	}

	sidebarW, contentW, bodyH, reqH, respH := m.layoutDimensions()
	_ = contentW
	_ = respH

	// 1. Mouse Wheel Scrolling
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown || msg.Button == tea.MouseButtonWheelLeft || msg.Button == tea.MouseButtonWheelRight {
		innerH := bodyH - 2
		if innerH < 1 {
			innerH = 1
		}

		// Over sidebar
		if msg.X < sidebarW && msg.Y >= 5 && msg.Y < 5+bodyH {
			if msg.Button == tea.MouseButtonWheelUp {
				if m.SidebarOffset > 0 {
					m.SidebarOffset--
				}
				if m.SelectedIdx > m.SidebarOffset+innerH-1 {
					m.SelectedIdx = m.SidebarOffset + innerH - 1
					m.selectCurrentItem()
				}
			} else if msg.Button == tea.MouseButtonWheelDown {
				maxOffset := len(m.VisibleItems) - innerH
				if maxOffset < 0 {
					maxOffset = 0
				}
				if m.SidebarOffset < maxOffset {
					m.SidebarOffset++
				}
				if m.SelectedIdx < m.SidebarOffset {
					m.SelectedIdx = m.SidebarOffset
					m.selectCurrentItem()
				}
			}
			return *m, nil
		}

		// Over Request Pane
		if msg.X >= sidebarW && msg.Y >= 5 && msg.Y < 5+reqH {
			if msg.Button == tea.MouseButtonWheelUp {
				if m.ReqScroll > 0 {
					m.ReqScroll--
				}
			} else if msg.Button == tea.MouseButtonWheelDown {
				m.ReqScroll++
			}
			return *m, nil
		}

		// Over Response Pane
		if msg.X >= sidebarW && msg.Y >= 5+reqH && msg.Y < 5+bodyH {
			if msg.Button == tea.MouseButtonWheelUp {
				if m.RespScroll > 0 {
					m.RespScroll -= 2
					if m.RespScroll < 0 {
						m.RespScroll = 0
					}
				}
			} else if msg.Button == tea.MouseButtonWheelDown {
				m.RespScroll += 2
			}
			return *m, nil
		}

		return *m, nil
	}

	// 2. Left Button Click
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		// A. Header (rows 0..4)
		if msg.Y >= 0 && msg.Y < 5 {
			if msg.Y == 2 {
				// Line 2: " <dog2>  Collection [C]: [name]   Env [E]: [env]"
				collEnd := 12 + 16 + len(m.Coll.Name) + 2
				if msg.X >= collEnd {
					m.cycleEnvironment()
				} else if msg.X >= 11 {
					m.StatusMsg = fmt.Sprintf("Collection: %s (%d requests)", m.Coll.Name, len(m.Coll.Requests))
				}
			}
			return *m, nil
		}

		// B. Sidebar (cols 0..sidebarW-1, rows 5..5+bodyH-1)
		if msg.X < sidebarW && msg.Y >= 5 && msg.Y < 5+bodyH {
			if msg.Y >= 6 && msg.Y < 5+bodyH-1 {
				row := msg.Y - 6
				targetIdx := m.SidebarOffset + row
				if targetIdx >= 0 && targetIdx < len(m.VisibleItems) {
					m.SelectedIdx = targetIdx
					it := m.VisibleItems[targetIdx]
					if it.Type == "folder" {
						m.ExpandedFolders[it.Folder.ID] = !m.ExpandedFolders[it.Folder.ID]
						m.rebuildTree()
					} else {
						m.IsEditingURL = false
						m.ACCandidates = nil
						m.selectCurrentItem()
					}
				}
			}
			return *m, nil
		}

		// C. Request Pane (cols sidebarW..Width-1, rows 5..5+reqH-1)
		if msg.X >= sidebarW && msg.Y >= 5 && msg.Y < 5+reqH {
			relX := msg.X - sidebarW - 1
			if relX < 0 {
				relX = 0
			}

			// Row 6: Method badge + URL
			if msg.Y == 6 && m.SelectedRequest != nil {
				methodBadgeLen := len(m.SelectedRequest.Method) + 5
				if relX <= methodBadgeLen {
					m.cycleMethod()
				} else {
					m.IsEditingURL = true
					m.URLEditText = m.SelectedRequest.URL
					clickCol := relX - (methodBadgeLen + 2)
					if clickCol < 0 {
						clickCol = 0
					}
					if clickCol > len(m.URLEditText) {
						clickCol = len(m.URLEditText)
					}
					m.URLCursor = clickCol
					m.updateAutocomplete()
				}
				return *m, nil
			}

			// Row 7: Request Tabs
			if msg.Y == 7 {
				m.IsEditingURL = false
				m.ACCandidates = nil
				if relX < 15 {
					m.ActiveTab = "headers"
				} else if relX < 28 {
					m.ActiveTab = "params"
				} else if relX < 39 {
					m.ActiveTab = "body"
				} else if relX < 50 {
					m.ActiveTab = "auth"
				} else {
					m.ActiveTab = "docs"
				}
				return *m, nil
			}

			return *m, nil
		}

		// D. Response Pane (cols sidebarW..Width-1, rows 5+reqH..5+bodyH-1)
		if msg.X >= sidebarW && msg.Y >= 5+reqH && msg.Y < 5+bodyH {
			relX := msg.X - sidebarW - 1
			// Row 5+reqH+1: Response tabs & status
			if msg.Y == 5+reqH+1 {
				if relX >= 38 && relX < 53 {
					m.ActiveRespTab = "body"
				} else if relX >= 53 {
					m.ActiveRespTab = "headers"
				} else if m.LastResponse == nil {
					m.executeCurrentRequest()
				}
			}
			return *m, nil
		}

		// E. Status Bar (last row)
		if msg.Y >= m.Height-1 {
			if len(m.ACCandidates) > 0 {
				if msg.X >= m.Width-18 {
					m.ACCandidates = nil
					m.ACSelectedIdx = 0
				} else {
					m.acceptAutocomplete()
				}
				return *m, nil
			}

			// Shortcuts:
			// " [Space] Send   [U] Edit URL   [E] Env   [M] Method   [1-5] Tabs   [S] Save   [q] Quit"
			if msg.X >= 1 && msg.X <= 15 {
				m.executeCurrentRequest()
			} else if msg.X >= 16 && msg.X <= 29 {
				if m.SelectedRequest != nil {
					m.IsEditingURL = true
					m.URLEditText = m.SelectedRequest.URL
					m.URLCursor = len(m.URLEditText)
					m.updateAutocomplete()
				}
			} else if msg.X >= 30 && msg.X <= 39 {
				m.cycleEnvironment()
			} else if msg.X >= 40 && msg.X <= 53 {
				m.cycleMethod()
			} else if msg.X >= 54 && msg.X <= 66 {
				tabs := []string{"headers", "params", "body", "auth", "docs"}
				curIdx := 0
				for i, t := range tabs {
					if t == m.ActiveTab {
						curIdx = i
						break
					}
				}
				m.ActiveTab = tabs[(curIdx+1)%len(tabs)]
			} else if msg.X >= 67 && msg.X <= 78 {
				_ = m.saveCurrentRequest()
			} else if msg.X >= 79 {
				return *m, tea.Quit
			}
			return *m, nil
		}
	}

	return *m, nil
}

func (m *Model) handleURLEditKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.IsEditingURL = false
		m.ACCandidates = nil
		m.ACSelectedIdx = 0
		return *m, nil

	case tea.KeyEnter:
		if len(m.ACCandidates) > 0 {
			m.acceptAutocomplete()
			return *m, nil
		}
		m.IsEditingURL = false
		if m.SelectedRequest != nil {
			if m.SelectedRequest.URL != m.URLEditText {
				m.SelectedRequest.URL = m.URLEditText
				m.ModifiedRequests[m.SelectedRequest.ID] = true
				m.StatusMsg = "URL updated. Press [S] to save to disk."
			}
		}
		return *m, nil

	case tea.KeyTab:
		if len(m.ACCandidates) > 0 {
			m.acceptAutocomplete()
		}
		return *m, nil

	case tea.KeyBackspace:
		if m.URLCursor > 0 && len(m.URLEditText) > 0 {
			m.URLEditText = m.URLEditText[:m.URLCursor-1] + m.URLEditText[m.URLCursor:]
			m.URLCursor--
			m.updateAutocomplete()
		}
		return *m, nil

	case tea.KeyDelete:
		if m.URLCursor < len(m.URLEditText) {
			m.URLEditText = m.URLEditText[:m.URLCursor] + m.URLEditText[m.URLCursor+1:]
			m.updateAutocomplete()
		}
		return *m, nil

	case tea.KeyLeft:
		if len(m.ACCandidates) > 0 {
			if m.ACSelectedIdx > 0 {
				m.ACSelectedIdx--
			} else {
				m.ACSelectedIdx = len(m.ACCandidates) - 1
			}
			return *m, nil
		}
		if m.URLCursor > 0 {
			m.URLCursor--
			m.updateAutocomplete()
		}
		return *m, nil

	case tea.KeyRight:
		if len(m.ACCandidates) > 0 {
			if m.ACSelectedIdx < len(m.ACCandidates)-1 {
				m.ACSelectedIdx++
			} else {
				m.ACSelectedIdx = 0
			}
			return *m, nil
		}
		if m.URLCursor < len(m.URLEditText) {
			m.URLCursor++
			m.updateAutocomplete()
		}
		return *m, nil

	case tea.KeyHome, tea.KeyCtrlA:
		m.URLCursor = 0
		m.updateAutocomplete()
		return *m, nil

	case tea.KeyEnd, tea.KeyCtrlE:
		m.URLCursor = len(m.URLEditText)
		m.updateAutocomplete()
		return *m, nil

	case tea.KeyRunes:
		ch := string(msg.Runes)
		m.URLEditText = m.URLEditText[:m.URLCursor] + ch + m.URLEditText[m.URLCursor:]
		m.URLCursor += len(ch)
		m.updateAutocomplete()
		return *m, nil
	}

	return *m, nil
}

func (m *Model) handleNormalKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return *m, tea.Quit

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

	case "enter":
		if m.SelectedIdx >= 0 && m.SelectedIdx < len(m.VisibleItems) {
			it := m.VisibleItems[m.SelectedIdx]
			if it.Type == "folder" {
				m.ExpandedFolders[it.Folder.ID] = !m.ExpandedFolders[it.Folder.ID]
				m.rebuildTree()
			} else {
				m.executeCurrentRequest()
			}
		}

	case " ":
		m.executeCurrentRequest()

	case "u", "U":
		if m.SelectedRequest != nil {
			m.IsEditingURL = true
			m.URLEditText = m.SelectedRequest.URL
			m.URLCursor = len(m.URLEditText)
			m.updateAutocomplete()
		}

	case "e", "E":
		m.cycleEnvironment()

	case "m", "M":
		m.cycleMethod()

	case "s", "S":
		_ = m.saveCurrentRequest()

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

	case "tab":
		if m.ActiveRespTab == "body" {
			m.ActiveRespTab = "headers"
		} else {
			m.ActiveRespTab = "body"
		}

	case "pgup", "ctrl+u":
		if m.RespScroll > 0 {
			m.RespScroll -= 5
			if m.RespScroll < 0 {
				m.RespScroll = 0
			}
		}

	case "pgdown", "ctrl+d":
		m.RespScroll += 5
	}

	return *m, nil
}

func (m *Model) executeCurrentRequest() {
	if m.SelectedRequest == nil {
		return
	}
	req := m.SelectedRequest
	m.StatusMsg = fmt.Sprintf("Sending %s %s...", req.Method, req.Name)
	_ = m.ScriptRunner.RunPreRequest(req)
	resp, err := bru.ExecuteRequest(req, m.Coll)
	if err != nil {
		m.StatusMsg = fmt.Sprintf("Error: %v", err)
	} else {
		m.LastResponse = resp
		_ = m.ScriptRunner.RunPostResponse(req, resp)
		m.RespScroll = 0
		m.StatusMsg = fmt.Sprintf("Done: %d %s (%dms)", resp.Status, resp.StatusText, resp.DurationMs)
	}
}

func (m *Model) selectCurrentItem() {
	if m.SelectedIdx >= 0 && m.SelectedIdx < len(m.VisibleItems) {
		it := m.VisibleItems[m.SelectedIdx]
		if it.Type == "request" {
			m.SelectedRequest = it.Request
			m.RespScroll = 0
		}
	}
}

func (m *Model) cycleEnvironment() {
	if len(m.Coll.Environments) == 0 {
		m.StatusMsg = "No environments defined"
		return
	}
	cur := m.Coll.ActiveEnvironment
	idx := -1
	for i, env := range m.Coll.Environments {
		if env.Name == cur {
			idx = i
			break
		}
	}
	nextIdx := (idx + 1) % len(m.Coll.Environments)
	m.Coll.ActiveEnvironment = m.Coll.Environments[nextIdx].Name
	m.StatusMsg = fmt.Sprintf("Environment switched to: [%s]", m.Coll.ActiveEnvironment)
}

func (m *Model) cycleMethod() {
	if m.SelectedRequest == nil {
		return
	}
	methods := []bru.HttpMethod{bru.MethodGet, bru.MethodPost, bru.MethodPut, bru.MethodPatch, bru.MethodDelete}
	idx := 0
	for i, meth := range methods {
		if meth == m.SelectedRequest.Method {
			idx = i
			break
		}
	}
	next := methods[(idx+1)%len(methods)]
	m.SelectedRequest.Method = next
	m.ModifiedRequests[m.SelectedRequest.ID] = true
	m.StatusMsg = fmt.Sprintf("Method changed to: %s", next)
}

func (m *Model) saveCurrentRequest() error {
	if m.SelectedRequest == nil || m.SelectedRequest.FilePath == "" {
		m.StatusMsg = "No request selected to save"
		return nil
	}
	raw := bru.SerializeRequest(m.SelectedRequest)
	err := os.WriteFile(m.SelectedRequest.FilePath, []byte(raw), 0644)
	if err != nil {
		m.StatusMsg = fmt.Sprintf("Save failed: %v", err)
		return err
	}
	delete(m.ModifiedRequests, m.SelectedRequest.ID)
	m.StatusMsg = fmt.Sprintf("Saved: %s", filepath.Base(m.SelectedRequest.FilePath))
	return nil
}

func (m *Model) getAutocompleteVariables() []AutocompleteCandidate {
	var list []AutocompleteCandidate
	seen := make(map[string]bool)

	dynamics := []string{"$uuid", "$guid", "guid", "uuid", "$timestamp", "timestamp", "$isoTimestamp", "$randomInt"}
	for _, d := range dynamics {
		list = append(list, AutocompleteCandidate{Name: d, Type: "dynamic", Val: "<dynamic>"})
		seen[d] = true
	}

	if m.Coll.ActiveEnvironment != "" {
		for _, env := range m.Coll.Environments {
			if env.Name == m.Coll.ActiveEnvironment {
				for k, v := range env.Variables {
					if !seen[k] {
						list = append(list, AutocompleteCandidate{Name: k, Type: "env", Val: v})
						seen[k] = true
					}
				}
			}
		}
	}

	for k, v := range m.Coll.RuntimeVariables {
		if !seen[k] {
			list = append(list, AutocompleteCandidate{Name: k, Type: "runtime", Val: fmt.Sprintf("%v", v)})
			seen[k] = true
		}
	}

	return list
}

func (m *Model) updateAutocomplete() {
	if !m.IsEditingURL {
		m.ACCandidates = nil
		m.ACSelectedIdx = 0
		return
	}

	beforeCursor := m.URLEditText[:m.URLCursor]
	idx := strings.LastIndex(beforeCursor, "{{")
	if idx == -1 {
		m.ACCandidates = nil
		m.ACSelectedIdx = 0
		return
	}

	if strings.Contains(beforeCursor[idx:], "}}") {
		m.ACCandidates = nil
		m.ACSelectedIdx = 0
		return
	}

	prefix := beforeCursor[idx+2:]
	for _, ch := range prefix {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-' || ch == '$') {
			m.ACCandidates = nil
			m.ACSelectedIdx = 0
			return
		}
	}

	m.ACMatchPrefix = prefix
	m.ACMatchStart = idx + 2
	prefixLower := strings.ToLower(prefix)

	allVars := m.getAutocompleteVariables()
	var filtered []AutocompleteCandidate
	for _, v := range allVars {
		if strings.Contains(strings.ToLower(v.Name), prefixLower) {
			filtered = append(filtered, v)
			if len(filtered) >= 6 {
				break
			}
		}
	}

	m.ACCandidates = filtered
	if m.ACSelectedIdx >= len(m.ACCandidates) {
		if len(m.ACCandidates) > 0 {
			m.ACSelectedIdx = len(m.ACCandidates) - 1
		} else {
			m.ACSelectedIdx = 0
		}
	}
}

func (m *Model) acceptAutocomplete() {
	if len(m.ACCandidates) == 0 || m.ACSelectedIdx >= len(m.ACCandidates) {
		return
	}
	cand := m.ACCandidates[m.ACSelectedIdx]

	before := m.URLEditText[:m.ACMatchStart]
	after := m.URLEditText[m.URLCursor:]
	hasClosing := strings.HasPrefix(after, "}}")
	insertion := cand.Name
	if !hasClosing {
		insertion += "}}"
	}

	m.URLEditText = before + insertion + after
	m.URLCursor = len(before) + len(insertion)
	if hasClosing {
		m.URLCursor += 2
	}
	m.ACCandidates = nil
	m.ACSelectedIdx = 0
}

func (m Model) View() string {
	if m.Width <= 0 || m.Height <= 0 {
		return "Loading brui..."
	}

	sidebarW, contentW, bodyH, _, _ := m.layoutDimensions()

	header := m.renderHeader(m.Width, 5)
	statusBar := m.renderStatusBar(m.Width)

	sidebar := m.renderSidebar(sidebarW, bodyH)
	content := m.renderContent(contentW, bodyH)

	body := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, content)

	return lipgloss.JoinVertical(lipgloss.Left, header, body, statusBar)
}

func (m Model) renderHeader(width, height int) string {
	E := lipgloss.Color("#c97a2b")
	Y := lipgloss.Color("#f4aa42")
	K := lipgloss.Color("#11111b")
	R := lipgloss.Color("#ea5a47")

	dog1 := lipgloss.NewStyle().Foreground(E).Render("▄█") +
		lipgloss.NewStyle().Foreground(Y).Render("█████") +
		lipgloss.NewStyle().Foreground(E).Render("█▄")

	dog2 := lipgloss.NewStyle().Foreground(E).Render("▀") +
		lipgloss.NewStyle().Foreground(E).Background(Y).Render("▀") +
		lipgloss.NewStyle().Foreground(K).Background(Y).Render("▀") +
		lipgloss.NewStyle().Foreground(Y).Render("█") +
		lipgloss.NewStyle().Foreground(Y).Background(K).Render("▀") +
		lipgloss.NewStyle().Foreground(Y).Render("█") +
		lipgloss.NewStyle().Foreground(K).Background(Y).Render("▀") +
		lipgloss.NewStyle().Foreground(E).Background(Y).Render("▀") +
		lipgloss.NewStyle().Foreground(E).Render("▀")

	dog3 := "  " +
		lipgloss.NewStyle().Foreground(Y).Render("▀▀") +
		lipgloss.NewStyle().Foreground(R).Render("█") +
		lipgloss.NewStyle().Foreground(Y).Render("▀▀") +
		"  "

	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Accent)).Bold(true)
	subtext := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Subtext))
	putStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Put)).Bold(true)
	optStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Success)).Bold(true)
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.BorderFocus))

	envName := m.Coll.ActiveEnvironment
	if envName == "" {
		envName = "None"
	}

	l1 := fmt.Sprintf(" %s  %s %s", dog1, accent.Render("BRUI"), subtext.Render("- Bruno Terminal Client (Go Native)"))
	l2 := fmt.Sprintf(" %s  %s %s   %s %s", dog2, subtext.Render("Collection [C]:"), putStyle.Render("["+m.Coll.Name+"]"), subtext.Render("Env [E]:"), optStyle.Render("["+envName+"]"))
	l3 := fmt.Sprintf(" %s  %s", dog3, hintStyle.Render("[↑/↓] Select   [Space] Send   [1-5] Tabs   [U] Edit URL   [E] Env   [S] Save   [q] Quit"))

	boxStyle := lipgloss.NewStyle().
		Width(width).
		MaxWidth(width).
		Height(height).
		MaxHeight(height).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.Theme.Border))

	return boxStyle.Render(strings.Join([]string{l1, l2, l3}, "\n"))
}

func (m Model) renderSidebar(width, height int) string {
	innerW := width - 2
	innerH := height - 2
	if innerH < 1 {
		innerH = 1
	}

	// Keep SelectedIdx in view
	offset := m.SidebarOffset
	if m.SelectedIdx < offset {
		offset = m.SelectedIdx
	} else if m.SelectedIdx >= offset+innerH {
		offset = m.SelectedIdx - innerH + 1
	}
	if offset < 0 {
		offset = 0
	}
	m.SidebarOffset = offset

	subtext := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Subtext))
	fStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Accent)).Bold(true)

	var lines []string
	for r := 0; r < innerH; r++ {
		idx := offset + r
		if idx >= len(m.VisibleItems) {
			lines = append(lines, "")
			continue
		}

		it := m.VisibleItems[idx]
		indent := strings.Repeat("  ", it.Depth)
		isSelected := (idx == m.SelectedIdx)

		var lineContent string
		if it.Type == "folder" {
			marker := "v"
			if !it.Expanded {
				marker = ">"
			}
			total := len(it.Folder.Requests) + len(it.Folder.Folders)
			lineContent = fmt.Sprintf("%s%s [%s] %s", indent, fStyle.Render(marker), it.Folder.Name, subtext.Render(fmt.Sprintf("(%d)", total)))
		} else {
			req := it.Request
			badgeColor := theme.MethodColor(string(req.Method), m.Theme)
			badge := lipgloss.NewStyle().Foreground(badgeColor).Bold(true).Render(fmt.Sprintf("%-4s", req.Method))
			name := req.Name
			if m.ModifiedRequests[req.ID] {
				name += " *"
			}
			lineContent = fmt.Sprintf("%s%s %s", indent, badge, name)
		}

		if isSelected {
			selStyle := lipgloss.NewStyle().
				Width(innerW).
				MaxWidth(innerW).
				Background(lipgloss.Color(m.Theme.Selection)).
				Foreground(lipgloss.Color(m.Theme.SelectionFg)).
				Bold(true)
			lines = append(lines, selStyle.Render(lineContent))
		} else {
			normStyle := lipgloss.NewStyle().
				Width(innerW).
				MaxWidth(innerW).
				Foreground(lipgloss.Color(m.Theme.Fg))
			lines = append(lines, normStyle.Render(lineContent))
		}
	}

	boxStyle := lipgloss.NewStyle().
		Width(width).
		MaxWidth(width).
		Height(height).
		MaxHeight(height).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.Theme.BorderFocus))

	return boxStyle.Render(strings.Join(lines, "\n"))
}

func (m Model) renderContent(width, height int) string {
	reqH := height * 52 / 100
	respH := height - reqH

	reqPane := m.renderRequestPane(width, reqH)
	respPane := m.renderResponsePane(width, respH)

	return lipgloss.JoinVertical(lipgloss.Left, reqPane, respPane)
}

func (m Model) renderRequestPane(width, height int) string {
	innerW := width - 2
	innerH := height - 2
	if innerH < 3 {
		innerH = 3
	}

	if m.SelectedRequest == nil {
		style := lipgloss.NewStyle().
			Width(width).
			MaxWidth(width).
			Height(height).
			MaxHeight(height).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(m.Theme.Border))
		return style.Render(" No request selected")
	}

	req := m.SelectedRequest
	badgeColor := theme.MethodColor(string(req.Method), m.Theme)
	badge := lipgloss.NewStyle().Foreground(badgeColor).Bold(true).Render(fmt.Sprintf("[ %s ]", req.Method))

	var urlDisplay string
	if m.IsEditingURL {
		curChar := " "
		if m.URLCursor < len(m.URLEditText) {
			curChar = string(m.URLEditText[m.URLCursor])
		}
		curBlock := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#11111b")).
			Background(lipgloss.Color(m.Theme.Accent)).
			Bold(true).
			Render(curChar)

		before := m.URLEditText[:m.URLCursor]
		after := ""
		if m.URLCursor < len(m.URLEditText) {
			after = m.URLEditText[m.URLCursor+1:]
		}
		urlDisplay = before + curBlock + after
	} else {
		urlDisplay = highlightVariables(req.URL, m.Theme)
	}

	line1 := fmt.Sprintf(" %s  %s", badge, urlDisplay)

	tabs := []string{"Headers", "Params", "Body", "Auth", "Docs"}
	var tabStrs []string
	for i, t := range tabs {
		tl := strings.ToLower(t)
		if tl == m.ActiveTab {
			tabStrs = append(tabStrs, lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Accent)).Bold(true).Render(fmt.Sprintf("[ %d:%s ]", i+1, t)))
		} else {
			tabStrs = append(tabStrs, lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Subtext)).Render(fmt.Sprintf("  %d:%s  ", i+1, t)))
		}
	}
	line2 := " " + strings.Join(tabStrs, " ")

	sepLine := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Border)).Render(strings.Repeat("─", innerW))

	var contentLines []string
	switch m.ActiveTab {
	case "headers":
		for _, h := range req.Headers {
			contentLines = append(contentLines, fmt.Sprintf(" %s: %s", lipgloss.NewStyle().Bold(true).Render(h.Name), h.Value))
		}
		if len(m.Coll.CollectionHeaders) > 0 {
			sub := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Subtext))
			contentLines = append(contentLines, sub.Render(fmt.Sprintf(" --- Inherited from collection.bru (%d) ---", len(m.Coll.CollectionHeaders))))
			for _, ch := range m.Coll.CollectionHeaders {
				contentLines = append(contentLines, sub.Render(fmt.Sprintf(" %s: %s", ch.Name, ch.Value)))
			}
		}
	case "params":
		for _, p := range req.Params {
			contentLines = append(contentLines, fmt.Sprintf(" %s = %s", lipgloss.NewStyle().Bold(true).Render(p.Name), p.Value))
		}
		if len(req.Params) == 0 {
			contentLines = append(contentLines, " (No query parameters)")
		}
	case "body":
		if req.BodyContent != "" {
			bodyRows := strings.Split(req.BodyContent, "\n")
			for _, br := range bodyRows {
				contentLines = append(contentLines, " "+br)
			}
		} else {
			contentLines = append(contentLines, " (Empty body)")
		}
	case "auth":
		contentLines = append(contentLines, fmt.Sprintf(" Auth Mode: [%s]", req.AuthType))
		if req.AuthBearerToken != "" {
			contentLines = append(contentLines, fmt.Sprintf(" Bearer Token: %s", highlightVariables(req.AuthBearerToken, m.Theme)))
		}
	case "docs":
		if req.Docs != "" {
			docRows := strings.Split(req.Docs, "\n")
			for _, dr := range docRows {
				contentLines = append(contentLines, " "+dr)
			}
		} else {
			contentLines = append(contentLines, " (No documentation)")
		}
	}

	bodyCapacity := innerH - 3
	if bodyCapacity < 0 {
		bodyCapacity = 0
	}

	var finalRows []string
	finalRows = append(finalRows, line1, line2, sepLine)
	for r := 0; r < bodyCapacity; r++ {
		if r < len(contentLines) {
			finalRows = append(finalRows, contentLines[r])
		} else {
			finalRows = append(finalRows, "")
		}
	}

	boxStyle := lipgloss.NewStyle().
		Width(width).
		MaxWidth(width).
		Height(height).
		MaxHeight(height).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.Theme.Border))

	return boxStyle.Render(strings.Join(finalRows, "\n"))
}

func (m Model) renderResponsePane(width, height int) string {
	innerW := width - 2
	innerH := height - 2
	if innerH < 2 {
		innerH = 2
	}

	var line1 string
	if m.LastResponse == nil {
		line1 = " Status: [ Idle ]  Press [Space] or [Enter] to send request."
	} else {
		r := m.LastResponse
		statusColor := m.Theme.Success
		if r.Status >= 400 {
			statusColor = m.Theme.Error
		}
		stBadge := lipgloss.NewStyle().Foreground(lipgloss.Color(statusColor)).Bold(true).Render(fmt.Sprintf("%d %s", r.Status, r.StatusText))
		sub := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Subtext))
		tabB := "[ 1:Body ]"
		tabH := "  2:Headers  "
		if m.ActiveRespTab == "headers" {
			tabB = "  1:Body  "
			tabH = "[ 2:Headers ]"
		}
		line1 = fmt.Sprintf(" %s   %s   %s   %s   %s", stBadge, sub.Render(fmt.Sprintf("Time: %dms", r.DurationMs)), sub.Render(fmt.Sprintf("Size: %dB", r.SizeBytes)), tabB, tabH)
	}

	sepLine := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Border)).Render(strings.Repeat("─", innerW))

	var contentLines []string
	if m.LastResponse != nil {
		if m.ActiveRespTab == "headers" {
			for k, v := range m.LastResponse.Headers {
				contentLines = append(contentLines, fmt.Sprintf(" %s: %s", k, v))
			}
		} else {
			bodyRows := strings.Split(m.LastResponse.Body, "\n")
			for _, br := range bodyRows {
				contentLines = append(contentLines, " "+br)
			}
		}
	}

	bodyCapacity := innerH - 2
	if bodyCapacity < 0 {
		bodyCapacity = 0
	}

	offset := m.RespScroll
	if offset > len(contentLines)-bodyCapacity && len(contentLines) > bodyCapacity {
		offset = len(contentLines) - bodyCapacity
	}
	if offset < 0 {
		offset = 0
	}

	var finalRows []string
	finalRows = append(finalRows, line1, sepLine)
	for r := 0; r < bodyCapacity; r++ {
		idx := offset + r
		if idx < len(contentLines) {
			finalRows = append(finalRows, contentLines[idx])
		} else {
			finalRows = append(finalRows, "")
		}
	}

	boxStyle := lipgloss.NewStyle().
		Width(width).
		MaxWidth(width).
		Height(height).
		MaxHeight(height).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.Theme.Border))

	return boxStyle.Render(strings.Join(finalRows, "\n"))
}

func (m Model) renderStatusBar(width int) string {
	barStyle := lipgloss.NewStyle().
		Width(width).
		MaxWidth(width).
		Height(1).
		MaxHeight(1).
		Background(lipgloss.Color(m.Theme.Surface))

	if len(m.ACCandidates) > 0 {
		t := m.Theme
		var cands []string
		for idx, cand := range m.ACCandidates {
			col := t.Put
			if cand.Type == "env" {
				col = t.Accent
			}
			label := fmt.Sprintf("{{%s}} (%s)", cand.Name, cand.Type)
			if idx == m.ACSelectedIdx {
				cands = append(cands, lipgloss.NewStyle().
					Background(lipgloss.Color(t.Selection)).
					Foreground(lipgloss.Color(t.SelectionFg)).
					Bold(true).
					Render(" "+label+" "))
			} else {
				cands = append(cands, lipgloss.NewStyle().
					Foreground(lipgloss.Color(col)).
					Render(" "+label+" "))
			}
		}
		acTitle := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Patch)).Bold(true).Render(" [Autocomplete] ")
		hints := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Subtext)).Render(" │ [Tab/Enter] Accept  [←/→] Select  [Esc] Cancel")
		return barStyle.Render(acTitle + strings.Join(cands, "") + hints)
	}

	shortcuts := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.Theme.Fg)).
		Render(" [Space] Send   [U] Edit URL   [E] Env   [M] Method   [1-5] Tabs   [S] Save   [q] Quit")

	status := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.Theme.Accent)).
		Bold(true).
		Render(m.StatusMsg)

	return barStyle.Render(fmt.Sprintf(" %s  │  %s", shortcuts, status))
}

func highlightVariables(text string, t theme.Theme) string {
	if text == "" {
		return ""
	}
	peach := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Patch)).Bold(true)
	return varRegex.ReplaceAllStringFunc(text, func(m string) string {
		return peach.Render(m)
	})
}
