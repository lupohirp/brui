package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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

	// Inline Tab Content Edit
	IsEditingContent bool
	ContentEditText  string
	ContentCursor    int
	ContentScroll    int

	// Autocomplete state
	ACCandidates  []AutocompleteCandidate
	ACSelectedIdx int
	ACMatchPrefix string
	ACMatchStart  int

	// Environment Modal
	IsEnvModalOpen      bool
	EnvModalSelectedIdx int
	EnvModalFocusPane   string // "envs" or "vars"
	EnvModalVarIdx      int
	IsEnvPromptOpen     bool
	EnvPromptTitle      string
	EnvPromptText       string
	EnvPromptCursor     int
	EnvPromptAction     string // "edit_var", "new_var_name", "new_var_val", "new_env"
	EnvPromptTempKey    string

	// New Request Prompt
	IsNewReqPromptOpen bool
	NewReqName         string
	NewReqMethod       bru.HttpMethod
	NewReqURL          string
	NewReqStep         int // 0: Name, 1: URL

	// Scrolling
	RespScroll int
	ReqScroll  int
}

func NewModel(coll *bru.BrunoCollection) Model {
	m := Model{
		Coll:              coll,
		Theme:             theme.Mocha,
		ActiveTab:         "headers",
		ActiveRespTab:     "body",
		ScriptRunner:      bru.NewScriptRunner(coll),
		StatusMsg:         "Ready",
		ExpandedFolders:   make(map[string]bool),
		ModifiedRequests:  make(map[string]bool),
		EnvModalFocusPane: "envs",
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
		if m.IsNewReqPromptOpen {
			return m.handleNewReqKeys(msg)
		}
		if m.IsEnvPromptOpen {
			return m.handleEnvPromptKeys(msg)
		}
		if m.IsEnvModalOpen {
			return m.handleEnvModalKeys(msg)
		}
		if m.IsEditingURL {
			return m.handleURLEditKeys(msg)
		}
		if m.IsEditingContent {
			return m.handleContentEditKeys(msg)
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

	if m.IsEnvModalOpen {
		return m.handleEnvModalMouse(msg)
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
					m.openEnvironmentModal()
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
				if m.IsEditingContent {
					m.startEditingContent()
				}
				return *m, nil
			}

			// Rows >= 8: Content area click to edit
			if msg.Y >= 8 && m.SelectedRequest != nil {
				m.startEditingContent()
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
			// " [Space] Send   [U] Edit URL   [E] Env   [I] Edit Tab   [N] New Req   [M] Method   [S] Save   [q] Quit"
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
				m.openEnvironmentModal()
			} else if msg.X >= 40 && msg.X <= 54 {
				if m.SelectedRequest != nil {
					m.startEditingContent()
				}
			} else if msg.X >= 55 && msg.X <= 67 {
				m.openNewRequestPrompt()
			} else if msg.X >= 68 && msg.X <= 79 {
				m.cycleMethod()
			} else if msg.X >= 80 && msg.X <= 90 {
				_ = m.saveCurrentRequest()
			} else if msg.X >= 91 {
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

func (m *Model) startEditingContent() {
	if m.SelectedRequest == nil {
		return
	}
	m.IsEditingContent = true
	m.IsEditingURL = false
	m.ACCandidates = nil

	req := m.SelectedRequest
	switch m.ActiveTab {
	case "headers":
		var lines []string
		for _, h := range req.Headers {
			if h.Enabled {
				lines = append(lines, fmt.Sprintf("%s: %s", h.Name, h.Value))
			} else {
				lines = append(lines, fmt.Sprintf("# %s: %s", h.Name, h.Value))
			}
		}
		m.ContentEditText = strings.Join(lines, "\n")
	case "params":
		var lines []string
		for _, p := range req.Params {
			if p.Enabled {
				lines = append(lines, fmt.Sprintf("%s = %s", p.Name, p.Value))
			} else {
				lines = append(lines, fmt.Sprintf("# %s = %s", p.Name, p.Value))
			}
		}
		m.ContentEditText = strings.Join(lines, "\n")
	case "body":
		m.ContentEditText = req.BodyContent
	case "auth":
		m.ContentEditText = req.AuthBearerToken
	case "docs":
		m.ContentEditText = req.Docs
	default:
		m.ContentEditText = ""
	}
	m.ContentCursor = len(m.ContentEditText)
	m.ContentScroll = 0
	m.StatusMsg = fmt.Sprintf("Editing %s. [Esc] Done  [Ctrl+S] Save  [Enter] Newline", m.ActiveTab)
}

func (m *Model) saveTabContent() {
	if m.SelectedRequest == nil {
		return
	}
	req := m.SelectedRequest
	switch m.ActiveTab {
	case "headers":
		var newHeaders []bru.BruHeader
		lines := strings.Split(m.ContentEditText, "\n")
		for _, l := range lines {
			trimmed := strings.TrimSpace(l)
			if trimmed == "" {
				continue
			}
			enabled := true
			if strings.HasPrefix(trimmed, "#") {
				enabled = false
				trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
			}
			idx := strings.Index(trimmed, ":")
			if idx > 0 {
				name := strings.TrimSpace(trimmed[:idx])
				val := strings.TrimSpace(trimmed[idx+1:])
				newHeaders = append(newHeaders, bru.BruHeader{
					Name:    name,
					Value:   val,
					Enabled: enabled,
				})
			}
		}
		req.Headers = newHeaders
		m.ModifiedRequests[req.ID] = true

	case "params":
		var newParams []bru.BruParam
		lines := strings.Split(m.ContentEditText, "\n")
		for _, l := range lines {
			trimmed := strings.TrimSpace(l)
			if trimmed == "" {
				continue
			}
			enabled := true
			if strings.HasPrefix(trimmed, "#") {
				enabled = false
				trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
			}
			idx := strings.Index(trimmed, "=")
			if idx > 0 {
				name := strings.TrimSpace(trimmed[:idx])
				val := strings.TrimSpace(trimmed[idx+1:])
				newParams = append(newParams, bru.BruParam{
					Name:    name,
					Value:   val,
					Enabled: enabled,
				})
			}
		}
		req.Params = newParams
		m.ModifiedRequests[req.ID] = true

	case "body":
		req.BodyContent = m.ContentEditText
		m.ModifiedRequests[req.ID] = true

	case "auth":
		req.AuthBearerToken = m.ContentEditText
		m.ModifiedRequests[req.ID] = true

	case "docs":
		req.Docs = m.ContentEditText
		m.ModifiedRequests[req.ID] = true
	}
}

func (m *Model) moveContentCursorUp() {
	if m.ContentCursor == 0 {
		return
	}
	text := m.ContentEditText
	cur := m.ContentCursor
	lineStart := strings.LastIndex(text[:cur], "\n")
	col := cur
	if lineStart != -1 {
		col = cur - (lineStart + 1)
	}
	if lineStart <= 0 {
		m.ContentCursor = 0
		return
	}
	prevLineEnd := lineStart
	prevLineStart := strings.LastIndex(text[:prevLineEnd], "\n")
	var prevLineLen int
	if prevLineStart == -1 {
		prevLineStart = 0
		prevLineLen = prevLineEnd
	} else {
		prevLineStart = prevLineStart + 1
		prevLineLen = prevLineEnd - prevLineStart
	}
	targetCol := col
	if targetCol > prevLineLen {
		targetCol = prevLineLen
	}
	m.ContentCursor = prevLineStart + targetCol
}

func (m *Model) moveContentCursorDown() {
	text := m.ContentEditText
	cur := m.ContentCursor
	if cur >= len(text) {
		return
	}
	lineStart := strings.LastIndex(text[:cur], "\n")
	col := cur
	if lineStart != -1 {
		col = cur - (lineStart + 1)
	}
	nextNL := strings.Index(text[cur:], "\n")
	if nextNL == -1 {
		m.ContentCursor = len(text)
		return
	}
	nextLineStart := cur + nextNL + 1
	nextNextNL := strings.Index(text[nextLineStart:], "\n")
	var nextLineLen int
	if nextNextNL == -1 {
		nextLineLen = len(text) - nextLineStart
	} else {
		nextLineLen = nextNextNL
	}
	targetCol := col
	if targetCol > nextLineLen {
		targetCol = nextLineLen
	}
	m.ContentCursor = nextLineStart + targetCol
}

func (m *Model) handleContentEditKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.saveTabContent()
		m.IsEditingContent = false
		m.StatusMsg = fmt.Sprintf("Finished editing %s. Press [S] to save to disk.", m.ActiveTab)
		return *m, nil

	case tea.KeyCtrlS:
		m.saveTabContent()
		_ = m.saveCurrentRequest()
		return *m, nil

	case tea.KeyEnter:
		m.ContentEditText = m.ContentEditText[:m.ContentCursor] + "\n" + m.ContentEditText[m.ContentCursor:]
		m.ContentCursor++
		m.saveTabContent()
		return *m, nil

	case tea.KeyBackspace:
		if m.ContentCursor > 0 && len(m.ContentEditText) > 0 {
			m.ContentEditText = m.ContentEditText[:m.ContentCursor-1] + m.ContentEditText[m.ContentCursor:]
			m.ContentCursor--
			m.saveTabContent()
		}
		return *m, nil

	case tea.KeyDelete:
		if m.ContentCursor < len(m.ContentEditText) {
			m.ContentEditText = m.ContentEditText[:m.ContentCursor] + m.ContentEditText[m.ContentCursor+1:]
			m.saveTabContent()
		}
		return *m, nil

	case tea.KeyLeft:
		if m.ContentCursor > 0 {
			m.ContentCursor--
		}
		return *m, nil

	case tea.KeyRight:
		if m.ContentCursor < len(m.ContentEditText) {
			m.ContentCursor++
		}
		return *m, nil

	case tea.KeyUp:
		m.moveContentCursorUp()
		return *m, nil

	case tea.KeyDown:
		m.moveContentCursorDown()
		return *m, nil

	case tea.KeyHome, tea.KeyCtrlA:
		text := m.ContentEditText
		cur := m.ContentCursor
		lineStart := strings.LastIndex(text[:cur], "\n")
		if lineStart == -1 {
			m.ContentCursor = 0
		} else {
			m.ContentCursor = lineStart + 1
		}
		return *m, nil

	case tea.KeyEnd, tea.KeyCtrlE:
		text := m.ContentEditText
		cur := m.ContentCursor
		nextNL := strings.Index(text[cur:], "\n")
		if nextNL == -1 {
			m.ContentCursor = len(text)
		} else {
			m.ContentCursor = cur + nextNL
		}
		return *m, nil

	case tea.KeyTab:
		indent := "  "
		m.ContentEditText = m.ContentEditText[:m.ContentCursor] + indent + m.ContentEditText[m.ContentCursor:]
		m.ContentCursor += len(indent)
		m.saveTabContent()
		return *m, nil

	case tea.KeyRunes:
		ch := string(msg.Runes)
		m.ContentEditText = m.ContentEditText[:m.ContentCursor] + ch + m.ContentEditText[m.ContentCursor:]
		m.ContentCursor += len(ch)
		m.saveTabContent()
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
		m.openEnvironmentModal()

	case "m", "M":
		m.cycleMethod()

	case "s", "S":
		_ = m.saveCurrentRequest()

	case "i", "I":
		if m.SelectedRequest != nil {
			m.startEditingContent()
		}

	case "n", "N", "ctrl+n":
		m.openNewRequestPrompt()

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

func (m *Model) openEnvironmentModal() {
	m.IsEnvModalOpen = true
	m.EnvModalFocusPane = "envs"
	m.EnvModalVarIdx = 0
	idx := 0
	for i, env := range m.Coll.Environments {
		if env.Name == m.Coll.ActiveEnvironment {
			idx = i + 1
			break
		}
	}
	m.EnvModalSelectedIdx = idx
	m.StatusMsg = "Environment Manager open. [Tab] Focus Vars  [↑/↓] Select  [Enter] Activate  [Esc] Close"
}

func (m *Model) getSelectedEnv() *bru.BrunoEnvironment {
	if m.EnvModalSelectedIdx > 0 && m.EnvModalSelectedIdx <= len(m.Coll.Environments) {
		return m.Coll.Environments[m.EnvModalSelectedIdx-1]
	}
	return nil
}

func (m *Model) getSelectedEnvVarKeys() []string {
	env := m.getSelectedEnv()
	if env == nil {
		return nil
	}
	var keys []string
	for k := range env.Variables {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (m *Model) saveEnvironment(env *bru.BrunoEnvironment) error {
	if env == nil {
		return nil
	}
	if env.FilePath == "" {
		envDir := filepath.Join(m.Coll.Path, "environments")
		_ = os.MkdirAll(envDir, 0755)
		env.FilePath = filepath.Join(envDir, env.Name+".bru")
	}
	raw := bru.SerializeEnvironment(env)
	err := os.WriteFile(env.FilePath, []byte(raw), 0644)
	if err != nil {
		m.StatusMsg = fmt.Sprintf("Failed to save env %s: %v", env.Name, err)
		return err
	}
	m.StatusMsg = fmt.Sprintf("Saved environment: %s", filepath.Base(env.FilePath))
	return nil
}

func (m *Model) handleEnvModalKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	totalEnvs := len(m.Coll.Environments) + 1

	switch msg.String() {
	case "esc", "q", "ctrl+c":
		if m.EnvModalFocusPane == "vars" {
			m.EnvModalFocusPane = "envs"
			m.StatusMsg = "Focused on Environments. [↑/↓] Select  [Enter] Activate  [Tab] Vars"
			return *m, nil
		}
		m.IsEnvModalOpen = false
		m.StatusMsg = "Ready"
		return *m, nil

	case "tab":
		if m.EnvModalFocusPane == "envs" {
			if m.EnvModalSelectedIdx > 0 && m.EnvModalSelectedIdx <= len(m.Coll.Environments) {
				m.EnvModalFocusPane = "vars"
				m.EnvModalVarIdx = 0
				m.StatusMsg = "Focused on Variables. [↑/↓] Select  [Enter] Edit  [N] Add  [D] Delete  [Tab] Envs"
			}
		} else {
			m.EnvModalFocusPane = "envs"
			m.StatusMsg = "Focused on Environments. [↑/↓] Select  [Enter] Activate  [Tab] Vars"
		}
		return *m, nil

	case "left", "h":
		m.EnvModalFocusPane = "envs"
		return *m, nil

	case "right", "l":
		if m.EnvModalSelectedIdx > 0 && m.EnvModalSelectedIdx <= len(m.Coll.Environments) {
			m.EnvModalFocusPane = "vars"
			m.EnvModalVarIdx = 0
		}
		return *m, nil

	case "up", "k":
		if m.EnvModalFocusPane == "vars" {
			if m.EnvModalVarIdx > 0 {
				m.EnvModalVarIdx--
			}
		} else {
			if m.EnvModalSelectedIdx > 0 {
				m.EnvModalSelectedIdx--
			}
		}
		return *m, nil

	case "down", "j":
		if m.EnvModalFocusPane == "vars" {
			varKeys := m.getSelectedEnvVarKeys()
			if m.EnvModalVarIdx < len(varKeys)-1 {
				m.EnvModalVarIdx++
			}
		} else {
			if m.EnvModalSelectedIdx < totalEnvs-1 {
				m.EnvModalSelectedIdx++
			}
		}
		return *m, nil

	case "enter", " ":
		if m.EnvModalFocusPane == "vars" {
			varKeys := m.getSelectedEnvVarKeys()
			if len(varKeys) > 0 && m.EnvModalVarIdx >= 0 && m.EnvModalVarIdx < len(varKeys) {
				key := varKeys[m.EnvModalVarIdx]
				env := m.getSelectedEnv()
				val := env.Variables[key]
				m.IsEnvPromptOpen = true
				m.EnvPromptTitle = fmt.Sprintf("EDIT VARIABLE [%s]", key)
				m.EnvPromptText = val
				m.EnvPromptCursor = len(val)
				m.EnvPromptAction = "edit_var"
				m.EnvPromptTempKey = key
			}
		} else {
			if m.EnvModalSelectedIdx == 0 {
				m.Coll.ActiveEnvironment = ""
				m.StatusMsg = "Environment disabled (No Environment)"
			} else {
				chosen := m.Coll.Environments[m.EnvModalSelectedIdx-1]
				m.Coll.ActiveEnvironment = chosen.Name
				m.StatusMsg = fmt.Sprintf("Environment switched to: [%s]", chosen.Name)
			}
		}
		return *m, nil

	case "n", "N":
		if m.EnvModalFocusPane == "vars" {
			if m.EnvModalSelectedIdx > 0 && m.EnvModalSelectedIdx <= len(m.Coll.Environments) {
				m.IsEnvPromptOpen = true
				m.EnvPromptTitle = "NEW VARIABLE NAME"
				m.EnvPromptText = ""
				m.EnvPromptCursor = 0
				m.EnvPromptAction = "new_var_name"
			}
		} else {
			m.IsEnvPromptOpen = true
			m.EnvPromptTitle = "CREATE NEW ENVIRONMENT"
			m.EnvPromptText = ""
			m.EnvPromptCursor = 0
			m.EnvPromptAction = "new_env"
		}
		return *m, nil

	case "d", "D", "delete":
		if m.EnvModalFocusPane == "vars" {
			varKeys := m.getSelectedEnvVarKeys()
			if len(varKeys) > 0 && m.EnvModalVarIdx >= 0 && m.EnvModalVarIdx < len(varKeys) {
				key := varKeys[m.EnvModalVarIdx]
				env := m.getSelectedEnv()
				delete(env.Variables, key)
				_ = m.saveEnvironment(env)
				if m.EnvModalVarIdx >= len(env.Variables) && m.EnvModalVarIdx > 0 {
					m.EnvModalVarIdx--
				}
				m.StatusMsg = fmt.Sprintf("Deleted variable [%s]", key)
			}
		}
		return *m, nil

	case "s", "S":
		if env := m.getSelectedEnv(); env != nil {
			_ = m.saveEnvironment(env)
		}
		return *m, nil
	}
	return *m, nil
}

func (m *Model) handleEnvPromptKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.IsEnvPromptOpen = false
		return *m, nil

	case tea.KeyEnter:
		val := m.EnvPromptText
		switch m.EnvPromptAction {
		case "edit_var":
			if m.EnvModalSelectedIdx > 0 && m.EnvModalSelectedIdx <= len(m.Coll.Environments) {
				env := m.Coll.Environments[m.EnvModalSelectedIdx-1]
				env.Variables[m.EnvPromptTempKey] = val
				_ = m.saveEnvironment(env)
				m.StatusMsg = fmt.Sprintf("Updated [%s] = %s", m.EnvPromptTempKey, val)
			}
			m.IsEnvPromptOpen = false

		case "new_var_name":
			key := strings.TrimSpace(val)
			if key != "" {
				m.EnvPromptTempKey = key
				m.EnvPromptTitle = fmt.Sprintf("VALUE FOR [%s]", key)
				m.EnvPromptText = ""
				m.EnvPromptCursor = 0
				m.EnvPromptAction = "new_var_val"
				return *m, nil
			}
			m.IsEnvPromptOpen = false

		case "new_var_val":
			if m.EnvModalSelectedIdx > 0 && m.EnvModalSelectedIdx <= len(m.Coll.Environments) {
				env := m.Coll.Environments[m.EnvModalSelectedIdx-1]
				env.Variables[m.EnvPromptTempKey] = val
				_ = m.saveEnvironment(env)
				m.StatusMsg = fmt.Sprintf("Added variable [%s]", m.EnvPromptTempKey)
			}
			m.IsEnvPromptOpen = false

		case "new_env":
			name := strings.TrimSpace(val)
			if name != "" {
				newEnv := &bru.BrunoEnvironment{
					Name:      name,
					Variables: make(map[string]string),
				}
				m.Coll.Environments = append(m.Coll.Environments, newEnv)
				_ = m.saveEnvironment(newEnv)
				m.EnvModalSelectedIdx = len(m.Coll.Environments)
				m.StatusMsg = fmt.Sprintf("Created environment [%s]", name)
			}
			m.IsEnvPromptOpen = false
		}
		return *m, nil

	case tea.KeyBackspace:
		if m.EnvPromptCursor > 0 && len(m.EnvPromptText) > 0 {
			m.EnvPromptText = m.EnvPromptText[:m.EnvPromptCursor-1] + m.EnvPromptText[m.EnvPromptCursor:]
			m.EnvPromptCursor--
		}
		return *m, nil

	case tea.KeyDelete:
		if m.EnvPromptCursor < len(m.EnvPromptText) {
			m.EnvPromptText = m.EnvPromptText[:m.EnvPromptCursor] + m.EnvPromptText[m.EnvPromptCursor+1:]
		}
		return *m, nil

	case tea.KeyLeft:
		if m.EnvPromptCursor > 0 {
			m.EnvPromptCursor--
		}
		return *m, nil

	case tea.KeyRight:
		if m.EnvPromptCursor < len(m.EnvPromptText) {
			m.EnvPromptCursor++
		}
		return *m, nil

	case tea.KeyHome, tea.KeyCtrlA:
		m.EnvPromptCursor = 0
		return *m, nil

	case tea.KeyEnd, tea.KeyCtrlE:
		m.EnvPromptCursor = len(m.EnvPromptText)
		return *m, nil

	case tea.KeyRunes:
		ch := string(msg.Runes)
		m.EnvPromptText = m.EnvPromptText[:m.EnvPromptCursor] + ch + m.EnvPromptText[m.EnvPromptCursor:]
		m.EnvPromptCursor += len(ch)
		return *m, nil
	}
	return *m, nil
}

func (m *Model) handleEnvModalMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	modalW := m.Width * 75 / 100
	if modalW < 54 {
		modalW = 54
	}
	if modalW > 95 {
		modalW = 95
	}
	modalH := m.Height * 65 / 100
	if modalH < 14 {
		modalH = 14
	}
	if modalH > 26 {
		modalH = 26
	}

	startRow := (m.Height - modalH) / 2
	startCol := (m.Width - modalW) / 2
	totalEnvs := len(m.Coll.Environments) + 1
	leftW := (modalW - 2) * 35 / 100
	if leftW < 18 {
		leftW = 18
	}

	if msg.Button == tea.MouseButtonWheelUp {
		if m.EnvModalFocusPane == "vars" {
			if m.EnvModalVarIdx > 0 {
				m.EnvModalVarIdx--
			}
		} else {
			if m.EnvModalSelectedIdx > 0 {
				m.EnvModalSelectedIdx--
			}
		}
		return *m, nil
	}
	if msg.Button == tea.MouseButtonWheelDown {
		if m.EnvModalFocusPane == "vars" {
			varKeys := m.getSelectedEnvVarKeys()
			if m.EnvModalVarIdx < len(varKeys)-1 {
				m.EnvModalVarIdx++
			}
		} else {
			if m.EnvModalSelectedIdx < totalEnvs-1 {
				m.EnvModalSelectedIdx++
			}
		}
		return *m, nil
	}

	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		if msg.X < startCol || msg.X >= startCol+modalW || msg.Y < startRow || msg.Y >= startRow+modalH {
			m.IsEnvModalOpen = false
			return *m, nil
		}

		relX := msg.X - startCol
		relY := msg.Y - startRow

		// Close button
		if relY <= 1 && relX >= modalW-14 {
			m.IsEnvModalOpen = false
			return *m, nil
		}

		// Left column (Envs)
		if relX <= leftW+1 {
			m.EnvModalFocusPane = "envs"
			if relY >= 2 && relY < modalH-2 {
				clickedEnvIdx := relY - 2
				if clickedEnvIdx >= 0 && clickedEnvIdx < totalEnvs {
					m.EnvModalSelectedIdx = clickedEnvIdx
					if clickedEnvIdx == 0 {
						m.Coll.ActiveEnvironment = ""
						m.StatusMsg = "Environment disabled (No Environment)"
					} else {
						chosen := m.Coll.Environments[clickedEnvIdx-1]
						m.Coll.ActiveEnvironment = chosen.Name
						m.StatusMsg = fmt.Sprintf("Environment switched to: [%s]", chosen.Name)
					}
					m.IsEnvModalOpen = false
					return *m, nil
				}
			}
		} else {
			// Right column (Vars)
			m.EnvModalFocusPane = "vars"
			varKeys := m.getSelectedEnvVarKeys()
			if relY >= 3 && relY < modalH-2 {
				clickedVarIdx := relY - 3
				if clickedVarIdx >= 0 && clickedVarIdx < len(varKeys) {
					m.EnvModalVarIdx = clickedVarIdx
					key := varKeys[clickedVarIdx]
					env := m.getSelectedEnv()
					val := env.Variables[key]
					m.IsEnvPromptOpen = true
					m.EnvPromptTitle = fmt.Sprintf("EDIT VARIABLE [%s]", key)
					m.EnvPromptText = val
					m.EnvPromptCursor = len(val)
					m.EnvPromptAction = "edit_var"
					m.EnvPromptTempKey = key
					return *m, nil
				}
			}
		}
	}
	return *m, nil
}

func (m *Model) openNewRequestPrompt() {
	m.IsNewReqPromptOpen = true
	m.NewReqStep = 0
	m.NewReqName = ""
	m.NewReqMethod = bru.MethodGet
	m.NewReqURL = "https://"
	m.StatusMsg = "New Request: enter details. [Tab] Next field  [Enter] Create  [Esc] Cancel"
}

func (m *Model) handleNewReqKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.IsNewReqPromptOpen = false
		m.StatusMsg = "Ready"
		return *m, nil

	case tea.KeyTab:
		m.NewReqStep = (m.NewReqStep + 1) % 3
		return *m, nil

	case tea.KeyShiftTab:
		m.NewReqStep = (m.NewReqStep + 2) % 3
		return *m, nil

	case tea.KeyEnter:
		if m.NewReqStep == 0 {
			if strings.TrimSpace(m.NewReqName) != "" {
				m.NewReqStep = 1
			}
			return *m, nil
		} else if m.NewReqStep == 1 {
			m.NewReqStep = 2
			return *m, nil
		} else {
			m.createNewRequest()
			m.IsNewReqPromptOpen = false
			return *m, nil
		}

	case tea.KeyLeft, tea.KeyRight:
		if m.NewReqStep == 1 {
			methods := []bru.HttpMethod{bru.MethodGet, bru.MethodPost, bru.MethodPut, bru.MethodPatch, bru.MethodDelete}
			idx := 0
			for i, meth := range methods {
				if meth == m.NewReqMethod {
					idx = i
					break
				}
			}
			if msg.Type == tea.KeyRight {
				m.NewReqMethod = methods[(idx+1)%len(methods)]
			} else {
				m.NewReqMethod = methods[(idx-1+len(methods))%len(methods)]
			}
			return *m, nil
		}

	case tea.KeyBackspace:
		if m.NewReqStep == 0 && len(m.NewReqName) > 0 {
			m.NewReqName = m.NewReqName[:len(m.NewReqName)-1]
		} else if m.NewReqStep == 2 && len(m.NewReqURL) > 0 {
			m.NewReqURL = m.NewReqURL[:len(m.NewReqURL)-1]
		}
		return *m, nil

	case tea.KeyRunes:
		ch := string(msg.Runes)
		if m.NewReqStep == 0 {
			m.NewReqName += ch
		} else if m.NewReqStep == 1 {
			switch strings.ToUpper(ch) {
			case "G":
				m.NewReqMethod = bru.MethodGet
			case "P":
				if m.NewReqMethod == bru.MethodPost {
					m.NewReqMethod = bru.MethodPut
				} else if m.NewReqMethod == bru.MethodPut {
					m.NewReqMethod = bru.MethodPatch
				} else {
					m.NewReqMethod = bru.MethodPost
				}
			case "D":
				m.NewReqMethod = bru.MethodDelete
			}
		} else if m.NewReqStep == 2 {
			m.NewReqURL += ch
		}
		return *m, nil
	}
	return *m, nil
}

func (m *Model) createNewRequest() {
	name := strings.TrimSpace(m.NewReqName)
	if name == "" {
		name = "New Request"
	}
	method := m.NewReqMethod
	if method == "" {
		method = bru.MethodGet
	}
	url := strings.TrimSpace(m.NewReqURL)

	targetDir := m.Coll.Path
	var targetFolder *bru.BrunoFolder

	if m.SelectedIdx >= 0 && m.SelectedIdx < len(m.VisibleItems) {
		it := m.VisibleItems[m.SelectedIdx]
		if it.Type == "folder" && it.Folder != nil {
			targetFolder = it.Folder
			if targetFolder.Path != "" {
				targetDir = targetFolder.Path
			}
		} else if it.Type == "request" && it.Request != nil && it.Request.FilePath != "" {
			targetDir = filepath.Dir(it.Request.FilePath)
			for _, f := range m.Coll.Folders {
				if f.Path == targetDir {
					targetFolder = f
					break
				}
			}
		}
	}

	slug := strings.ToLower(strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			return r
		}
		if r == ' ' {
			return '-'
		}
		return -1
	}, name))
	if slug == "" {
		slug = "request"
	}

	filename := slug + ".bru"
	fullPath := filepath.Join(targetDir, filename)

	counter := 1
	for {
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			break
		}
		filename = fmt.Sprintf("%s-%d.bru", slug, counter)
		fullPath = filepath.Join(targetDir, filename)
		counter++
	}

	newReq := &bru.BrunoRequest{
		ID:          strings.TrimSuffix(filename, ".bru"),
		Name:        name,
		Filename:    filename,
		FilePath:    fullPath,
		Method:      method,
		URL:         url,
		BodyType:    "json",
		BodyContent: "{\n  \n}",
		Headers:     []bru.BruHeader{},
		Params:      []bru.BruParam{},
	}

	raw := bru.SerializeRequest(newReq)
	if err := os.WriteFile(fullPath, []byte(raw), 0644); err != nil {
		m.StatusMsg = fmt.Sprintf("Error creating request file: %v", err)
		return
	}

	m.Coll.Requests = append(m.Coll.Requests, newReq)
	if targetFolder != nil {
		targetFolder.Requests = append(targetFolder.Requests, newReq)
	}

	m.rebuildTree()

	for idx, item := range m.VisibleItems {
		if item.Type == "request" && item.Request != nil && item.Request.FilePath == fullPath {
			m.SelectedIdx = idx
			m.selectCurrentItem()
			break
		}
	}

	m.StatusMsg = fmt.Sprintf("Created %s %s [%s]", method, name, filename)
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
	base := lipgloss.JoinVertical(lipgloss.Left, header, body, statusBar)

	if m.IsNewReqPromptOpen {
		modal := m.renderNewReqModal()
		return overlayModal(base, modal, m.Width, m.Height)
	}

	if m.IsEnvPromptOpen {
		modal := m.renderPromptModal()
		return overlayModal(base, modal, m.Width, m.Height)
	}

	if m.IsEnvModalOpen {
		modal := m.renderEnvironmentModal()
		return overlayModal(base, modal, m.Width, m.Height)
	}

	return base
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
		Width(width - 2).
		MaxWidth(width).
		Height(height - 2).
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
			fName := it.Folder.Name
			countStr := fmt.Sprintf(" (%d)", total)
			prefixLen := len(indent) + 2 + 1 // indent + "v " + "["
			suffixLen := 1 + len(countStr)   // "]" + countStr
			avail := innerW - prefixLen - suffixLen
			if avail < 0 {
				avail = 0
			}
			if len(fName) > avail {
				if avail > 1 {
					fName = fName[:avail-1] + "…"
				} else {
					fName = fName[:avail]
				}
			}

			if isSelected {
				mStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Accent)).Background(lipgloss.Color(m.Theme.Selection)).Bold(true)
				sStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Subtext)).Background(lipgloss.Color(m.Theme.Selection))
				lineContent = fmt.Sprintf("%s%s [%s]%s", indent, mStyle.Render(marker), fName, sStyle.Render(countStr))
			} else {
				lineContent = fmt.Sprintf("%s%s [%s]%s", indent, fStyle.Render(marker), fName, subtext.Render(countStr))
			}
		} else {
			req := it.Request
			badgeColor := theme.MethodColor(string(req.Method), m.Theme)
			badgeText := fmt.Sprintf("%-4s", req.Method)
			name := req.Name
			modIndicator := ""
			if m.ModifiedRequests[req.ID] {
				modIndicator = " *"
			}
			prefixLen := len(indent) + 4 + 1
			avail := innerW - prefixLen - len(modIndicator)
			if avail < 0 {
				avail = 0
			}
			if len(name) > avail {
				if avail > 1 {
					name = name[:avail-1] + "…"
				} else {
					name = name[:avail]
				}
			}

			if isSelected {
				badge := lipgloss.NewStyle().
					Foreground(badgeColor).
					Background(lipgloss.Color(m.Theme.Selection)).
					Bold(true).
					Render(badgeText)
				lineContent = fmt.Sprintf("%s%s %s%s", indent, badge, name, modIndicator)
			} else {
				badge := lipgloss.NewStyle().
					Foreground(badgeColor).
					Bold(true).
					Render(badgeText)
				lineContent = fmt.Sprintf("%s%s %s%s", indent, badge, name, modIndicator)
			}
		}

		truncated := ansi.Truncate(lineContent, innerW, "")
		visW := ansi.StringWidth(truncated)
		padded := truncated
		if visW < innerW {
			padded += strings.Repeat(" ", innerW-visW)
		}

		if isSelected {
			selStyle := lipgloss.NewStyle().
				Background(lipgloss.Color(m.Theme.Selection)).
				Foreground(lipgloss.Color(m.Theme.SelectionFg)).
				Bold(true)
			lines = append(lines, selStyle.Render(padded))
		} else {
			normStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(m.Theme.Fg))
			lines = append(lines, normStyle.Render(padded))
		}
	}

	boxStyle := lipgloss.NewStyle().
		Width(width - 2).
		MaxWidth(width).
		Height(height - 2).
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
	if m.IsEditingContent {
		editBanner := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Patch)).Bold(true).Render(fmt.Sprintf("── EDITING [%s] (Esc: Done, Ctrl+S: Save) ", strings.ToUpper(m.ActiveTab)))
		bannerLen := ansi.StringWidth(editBanner)
		trailingLen := innerW - bannerLen
		if trailingLen < 0 {
			trailingLen = 0
		}
		editSep := editBanner + lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Border)).Render(strings.Repeat("─", trailingLen))
		finalRows = append(finalRows, line1, line2, editSep)

		rawLines := strings.Split(m.ContentEditText, "\n")
		cur := m.ContentCursor
		curLine := 0
		curCol := 0
		charCount := 0
		for idx, l := range rawLines {
			lineLen := len(l)
			if cur >= charCount && cur <= charCount+lineLen {
				curLine = idx
				curCol = cur - charCount
				break
			}
			charCount += lineLen + 1
		}

		offset := m.ContentScroll
		if curLine < offset {
			offset = curLine
		} else if curLine >= offset+bodyCapacity {
			offset = curLine - bodyCapacity + 1
		}
		if offset < 0 {
			offset = 0
		}
		m.ContentScroll = offset

		for r := 0; r < bodyCapacity; r++ {
			idx := offset + r
			if idx < len(rawLines) {
				l := rawLines[idx]
				if idx == curLine {
					var cBefore, cChar, cAfter string
					if curCol < len(l) {
						cBefore = l[:curCol]
						cChar = string(l[curCol])
						cAfter = l[curCol+1:]
					} else {
						cBefore = l
						cChar = " "
						cAfter = ""
					}
					curBlock := lipgloss.NewStyle().
						Foreground(lipgloss.Color("#11111b")).
						Background(lipgloss.Color(m.Theme.Accent)).
						Bold(true).
						Render(cChar)
					renderedLine := " " + cBefore + curBlock + cAfter
					finalRows = append(finalRows, ansi.Truncate(renderedLine, innerW, ""))
				} else {
					finalRows = append(finalRows, ansi.Truncate(" "+l, innerW, ""))
				}
			} else {
				finalRows = append(finalRows, "")
			}
		}
	} else {
		hint := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Subtext)).Render("── [I] Edit Content ")
		hLen := ansi.StringWidth(hint)
		trailingLen := innerW - hLen
		if trailingLen < 0 {
			trailingLen = 0
		}
		normalSep := hint + lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Border)).Render(strings.Repeat("─", trailingLen))
		finalRows = append(finalRows, line1, line2, normalSep)
		for r := 0; r < bodyCapacity; r++ {
			if r < len(contentLines) {
				finalRows = append(finalRows, contentLines[r])
			} else {
				finalRows = append(finalRows, "")
			}
		}
	}

	borderColor := m.Theme.Border
	if m.IsEditingContent {
		borderColor = m.Theme.BorderFocus
	}
	boxStyle := lipgloss.NewStyle().
		Width(width - 2).
		MaxWidth(width).
		Height(height - 2).
		MaxHeight(height).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor))

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
		Width(width - 2).
		MaxWidth(width).
		Height(height - 2).
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
		Render(" [Space] Send   [U] Edit URL   [E] Env   [I] Edit Tab   [N] New Req   [M] Method   [S] Save   [q] Quit")

	status := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.Theme.Accent)).
		Bold(true).
		Render(m.StatusMsg)

	barText := fmt.Sprintf(" %s  │  %s", shortcuts, status)
	w := ansi.StringWidth(barText)
	if w < width {
		barText += strings.Repeat(" ", width-w)
	}

	return barStyle.Render(barText)
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

func (m Model) renderEnvironmentModal() string {
	modalW := m.Width * 75 / 100
	if modalW < 54 {
		modalW = 54
	}
	if modalW > 95 {
		modalW = 95
	}
	modalH := m.Height * 65 / 100
	if modalH < 14 {
		modalH = 14
	}
	if modalH > 26 {
		modalH = 26
	}

	innerW := modalW - 2
	innerH := modalH - 2
	modalBg := lipgloss.Color("#181825")

	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Accent)).Background(modalBg).Bold(true)
	subtext := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Subtext)).Background(modalBg)
	activeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Success)).Background(modalBg).Bold(true)
	closeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Delete)).Background(modalBg).Bold(true)

	activeName := m.Coll.ActiveEnvironment
	if activeName == "" {
		activeName = "None"
	}
	titleLeft := fmt.Sprintf(" %s  %s %s", accent.Render("ENVIRONMENT & VARIABLES MANAGER"), subtext.Render("Active:"), activeStyle.Render("["+activeName+"]"))
	closeBtn := closeStyle.Render("[Esc Close]")
	titleSpace := innerW - ansi.StringWidth(titleLeft) - ansi.StringWidth(closeBtn) - 1
	if titleSpace < 1 {
		titleSpace = 1
	}
	row0Text := titleLeft + strings.Repeat(" ", titleSpace) + closeBtn
	row0 := lipgloss.NewStyle().Width(innerW).MaxWidth(innerW).Background(modalBg).Render(row0Text)

	row1 := lipgloss.NewStyle().Width(innerW).MaxWidth(innerW).Background(modalBg).Foreground(lipgloss.Color(m.Theme.Border)).Render(strings.Repeat("─", innerW))

	leftW := innerW * 35 / 100
	if leftW < 18 {
		leftW = 18
	}
	rightW := innerW - leftW - 1

	contentH := innerH - 3
	if contentH < 1 {
		contentH = 1
	}

	type envItem struct {
		Name     string
		IsActive bool
		Env      *bru.BrunoEnvironment
	}
	var items []envItem
	items = append(items, envItem{Name: "No Environment", IsActive: (m.Coll.ActiveEnvironment == ""), Env: nil})
	for _, env := range m.Coll.Environments {
		items = append(items, envItem{Name: env.Name, IsActive: (m.Coll.ActiveEnvironment == env.Name), Env: env})
	}

	var leftLines []string
	for r := 0; r < contentH; r++ {
		if r < len(items) {
			it := items[r]
			activeTag := ""
			if it.IsActive {
				activeTag = " " + activeStyle.Render("[Active]")
			}
			label := it.Name
			maxLabel := leftW - ansi.StringWidth(activeTag) - 2
			if maxLabel < 1 {
				maxLabel = 1
			}
			if len(label) > maxLabel {
				label = label[:maxLabel-1] + "…"
			}
			rowText := " " + label + activeTag

			if r == m.EnvModalSelectedIdx {
				sel := lipgloss.NewStyle().
					Width(leftW).
					MaxWidth(leftW).
					Height(1).
					MaxHeight(1).
					Background(lipgloss.Color(m.Theme.Selection)).
					Foreground(lipgloss.Color(m.Theme.SelectionFg)).
					Bold(true)
				leftLines = append(leftLines, sel.Render(rowText))
			} else {
				norm := lipgloss.NewStyle().
					Width(leftW).
					MaxWidth(leftW).
					Height(1).
					MaxHeight(1).
					Background(modalBg).
					Foreground(lipgloss.Color(m.Theme.Fg))
				leftLines = append(leftLines, norm.Render(rowText))
			}
		} else {
			leftLines = append(leftLines, lipgloss.NewStyle().Width(leftW).MaxWidth(leftW).Height(1).MaxHeight(1).Background(modalBg).Render(strings.Repeat(" ", leftW)))
		}
	}

	var rightLines []string
	var selectedEnv *bru.BrunoEnvironment
	if m.EnvModalSelectedIdx > 0 && m.EnvModalSelectedIdx <= len(m.Coll.Environments) {
		selectedEnv = m.Coll.Environments[m.EnvModalSelectedIdx-1]
	}

	var varKeys []string
	if selectedEnv != nil {
		for k := range selectedEnv.Variables {
			varKeys = append(varKeys, k)
		}
		sort.Strings(varKeys)
	}

	if selectedEnv == nil {
		rightLines = append(rightLines, subtext.Render(" (No environment active - variables disabled)"))
	} else {
		var rHeader string
		if m.EnvModalFocusPane == "vars" {
			rHeader = accent.Render(fmt.Sprintf(" Variables in [%s] (%d) [*]:", selectedEnv.Name, len(varKeys)))
		} else {
			rHeader = subtext.Render(fmt.Sprintf(" Variables in [%s] (%d) [Tab]:", selectedEnv.Name, len(varKeys)))
		}
		rightLines = append(rightLines, rHeader)

		for _, k := range varKeys {
			val := selectedEnv.Variables[k]
			line := fmt.Sprintf(" %s: %s", k, val)
			if ansi.StringWidth(line) > rightW {
				maxVal := rightW - len(k) - 4
				if maxVal > 3 {
					val = val[:maxVal-1] + "…"
				}
				line = fmt.Sprintf(" %s: %s", k, val)
			}
			rightLines = append(rightLines, line)
		}
	}

	var finalRightLines []string
	for r := 0; r < contentH; r++ {
		if r < len(rightLines) {
			lineText := rightLines[r]
			isVarSelected := (m.EnvModalFocusPane == "vars" && r > 0 && r-1 == m.EnvModalVarIdx)
			if isVarSelected {
				sel := lipgloss.NewStyle().
					Width(rightW).
					MaxWidth(rightW).
					Height(1).
					MaxHeight(1).
					Background(lipgloss.Color(m.Theme.Selection)).
					Foreground(lipgloss.Color(m.Theme.SelectionFg)).
					Bold(true)
				finalRightLines = append(finalRightLines, sel.Render(lineText))
			} else {
				norm := lipgloss.NewStyle().
					Width(rightW).
					MaxWidth(rightW).
					Height(1).
					MaxHeight(1).
					Background(modalBg).
					Foreground(lipgloss.Color(m.Theme.Fg))
				finalRightLines = append(finalRightLines, norm.Render(lineText))
			}
		} else {
			finalRightLines = append(finalRightLines, lipgloss.NewStyle().Width(rightW).MaxWidth(rightW).Height(1).MaxHeight(1).Background(modalBg).Render(strings.Repeat(" ", rightW)))
		}
	}

	sepCol := lipgloss.NewStyle().Background(modalBg).Foreground(lipgloss.Color(m.Theme.Border)).Render("│")
	var middleRows []string
	for r := 0; r < contentH; r++ {
		middleRows = append(middleRows, leftLines[r]+sepCol+finalRightLines[r])
	}

	var footer string
	if m.EnvModalFocusPane == "envs" {
		footer = subtext.Render(" [Tab] Focus Vars   [↑/↓] Select   [Enter] Activate   [N] New Env   [Esc] Close")
	} else {
		footer = accent.Render(" [Tab] Focus Envs   [↑/↓] Select   [Enter] Edit Value   [N] Add Var   [D] Delete   [S] Save")
	}
	footerLine := lipgloss.NewStyle().Width(innerW).MaxWidth(innerW).Background(modalBg).Render(" " + footer)

	allModalRows := []string{row0, row1}
	allModalRows = append(allModalRows, middleRows...)
	allModalRows = append(allModalRows, footerLine)

	modalBoxStyle := lipgloss.NewStyle().
		Width(modalW - 2).
		MaxWidth(modalW).
		Height(modalH - 2).
		MaxHeight(modalH).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.Theme.Accent)).
		BorderBackground(modalBg).
		Background(modalBg)

	return modalBoxStyle.Render(strings.Join(allModalRows, "\n"))
}

func (m Model) renderPromptModal() string {
	modalW := 56
	if modalW > m.Width-4 {
		modalW = m.Width - 4
	}
	modalH := 8
	innerW := modalW - 2
	modalBg := lipgloss.Color("#181825")
	inputBg := lipgloss.Color("#1e1e2e")

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Accent)).Background(modalBg).Bold(true)
	subtext := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Subtext)).Background(modalBg)

	title := titleStyle.Render(" " + m.EnvPromptTitle)
	sep := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Border)).Background(modalBg).Render(strings.Repeat("─", innerW))

	curChar := " "
	if m.EnvPromptCursor < len(m.EnvPromptText) {
		curChar = string(m.EnvPromptText[m.EnvPromptCursor])
	}
	curBlock := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#11111b")).
		Background(lipgloss.Color(m.Theme.Accent)).
		Bold(true).
		Render(curChar)

	before := m.EnvPromptText[:m.EnvPromptCursor]
	after := ""
	if m.EnvPromptCursor < len(m.EnvPromptText) {
		after = m.EnvPromptText[m.EnvPromptCursor+1:]
	}
	inputContent := before + curBlock + after
	inputLine := lipgloss.NewStyle().
		Width(innerW - 4).
		MaxWidth(innerW - 4).
		Background(inputBg).
		Foreground(lipgloss.Color(m.Theme.Fg)).
		Render(" " + inputContent)

	inputBox := lipgloss.NewStyle().
		Width(innerW).
		MaxWidth(innerW).
		Background(modalBg).
		Render("  " + inputLine)

	footer := subtext.Render(" [Enter] Confirm   [Esc] Cancel")
	footerLine := lipgloss.NewStyle().Width(innerW).MaxWidth(innerW).Background(modalBg).Render(" " + footer)

	lines := []string{
		lipgloss.NewStyle().Width(innerW).MaxWidth(innerW).Background(modalBg).Render(title),
		sep,
		lipgloss.NewStyle().Width(innerW).MaxWidth(innerW).Background(modalBg).Render(""),
		inputBox,
		lipgloss.NewStyle().Width(innerW).MaxWidth(innerW).Background(modalBg).Render(""),
		footerLine,
	}

	modalBoxStyle := lipgloss.NewStyle().
		Width(modalW - 2).
		MaxWidth(modalW).
		Height(modalH - 2).
		MaxHeight(modalH).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.Theme.Accent)).
		BorderBackground(modalBg).
		Background(modalBg)

	return modalBoxStyle.Render(strings.Join(lines, "\n"))
}

func (m Model) renderNewReqModal() string {
	modalW := 60
	if modalW > m.Width-4 {
		modalW = m.Width - 4
	}
	modalH := 12
	innerW := modalW - 2
	modalBg := lipgloss.Color("#181825")
	inputBg := lipgloss.Color("#1e1e2e")

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Accent)).Background(modalBg).Bold(true)
	subtext := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Subtext)).Background(modalBg)
	activeLabel := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Accent)).Background(modalBg).Bold(true)
	inactiveLabel := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Fg)).Background(modalBg)

	title := titleStyle.Render(" CREATE NEW REQUEST")
	sep := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Theme.Border)).Background(modalBg).Render(strings.Repeat("─", innerW))

	// Step 0: Name
	nameLbl := inactiveLabel.Render(" Name:   ")
	if m.NewReqStep == 0 {
		nameLbl = activeLabel.Render(" Name: > ")
	}
	nameInp := lipgloss.NewStyle().Width(innerW - 14).MaxWidth(innerW - 14).Background(inputBg).Render(" " + m.NewReqName + "█")
	nameRow := lipgloss.NewStyle().Width(innerW).MaxWidth(innerW).Background(modalBg).Render(" " + nameLbl + nameInp)

	// Step 1: Method
	methLbl := inactiveLabel.Render(" Method: ")
	if m.NewReqStep == 1 {
		methLbl = activeLabel.Render(" Method:> ")
	}
	methods := []bru.HttpMethod{bru.MethodGet, bru.MethodPost, bru.MethodPut, bru.MethodPatch, bru.MethodDelete}
	var methBadges []string
	for _, meth := range methods {
		if meth == m.NewReqMethod {
			methBadges = append(methBadges, lipgloss.NewStyle().
				Foreground(lipgloss.Color("#11111b")).
				Background(theme.MethodColor(string(meth), m.Theme)).
				Bold(true).
				Render(fmt.Sprintf(" %s ", meth)))
		} else {
			methBadges = append(methBadges, lipgloss.NewStyle().
				Foreground(lipgloss.Color(m.Theme.Subtext)).
				Background(inputBg).
				Render(fmt.Sprintf(" %s ", meth)))
		}
	}
	methHint := ""
	if m.NewReqStep == 1 {
		methHint = " " + subtext.Render("[←/→ Cycle]")
	}
	methRow := lipgloss.NewStyle().Width(innerW).MaxWidth(innerW).Background(modalBg).Render(" " + methLbl + strings.Join(methBadges, " ") + methHint)

	// Step 2: URL
	urlLbl := inactiveLabel.Render(" URL:    ")
	if m.NewReqStep == 2 {
		urlLbl = activeLabel.Render(" URL:  > ")
	}
	urlInp := lipgloss.NewStyle().Width(innerW - 14).MaxWidth(innerW - 14).Background(inputBg).Render(" " + m.NewReqURL + "█")
	urlRow := lipgloss.NewStyle().Width(innerW).MaxWidth(innerW).Background(modalBg).Render(" " + urlLbl + urlInp)

	footer := subtext.Render(" [Tab] Next field   [Enter] Confirm/Create   [Esc] Cancel")
	footerLine := lipgloss.NewStyle().Width(innerW).MaxWidth(innerW).Background(modalBg).Render(" " + footer)

	lines := []string{
		lipgloss.NewStyle().Width(innerW).MaxWidth(innerW).Background(modalBg).Render(title),
		sep,
		lipgloss.NewStyle().Width(innerW).MaxWidth(innerW).Background(modalBg).Render(""),
		nameRow,
		lipgloss.NewStyle().Width(innerW).MaxWidth(innerW).Background(modalBg).Render(""),
		methRow,
		lipgloss.NewStyle().Width(innerW).MaxWidth(innerW).Background(modalBg).Render(""),
		urlRow,
		lipgloss.NewStyle().Width(innerW).MaxWidth(innerW).Background(modalBg).Render(""),
		footerLine,
	}

	modalBoxStyle := lipgloss.NewStyle().
		Width(modalW - 2).
		MaxWidth(modalW).
		Height(modalH - 2).
		MaxHeight(modalH).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.Theme.Accent)).
		BorderBackground(modalBg).
		Background(modalBg)

	return modalBoxStyle.Render(strings.Join(lines, "\n"))
}


func overlayModal(base string, modal string, screenW, screenH int) string {
	baseLines := strings.Split(base, "\n")
	modalLines := strings.Split(modal, "\n")

	modalH := len(modalLines)
	modalW := ansi.StringWidth(modalLines[0])

	startRow := (screenH - modalH) / 2
	startCol := (screenW - modalW) / 2
	if startRow < 0 {
		startRow = 0
	}
	if startCol < 0 {
		startCol = 0
	}

	for r := 0; r < modalH && startRow+r < len(baseLines); r++ {
		mLine := modalLines[r]
		bLine := baseLines[startRow+r]

		left := ansi.Truncate(bLine, startCol, "")
		leftW := ansi.StringWidth(left)
		if leftW < startCol {
			left += strings.Repeat(" ", startCol-leftW)
		}

		right := ansi.TruncateLeft(bLine, startCol+modalW, "")
		rightW := ansi.StringWidth(right)
		remain := screenW - (startCol + modalW)
		if remain > 0 && rightW < remain {
			right += strings.Repeat(" ", remain-rightW)
		}

		baseLines[startRow+r] = left + mLine + right
	}

	return strings.Join(baseLines, "\n")
}
