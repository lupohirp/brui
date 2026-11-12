package ui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lupohirp/brui/pkg/bru"
)

func TestDogSprite(t *testing.T) {
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

	w1 := lipgloss.Width(dog1)
	w2 := lipgloss.Width(dog2)
	w3 := lipgloss.Width(dog3)

	if w1 != 9 || w2 != 9 || w3 != 9 {
		t.Fatalf("Dog sprite widths mismatch: w1=%d w2=%d w3=%d", w1, w2, w3)
	}
}

func TestModelViewStrictDimensions(t *testing.T) {
	coll := &bru.BrunoCollection{
		Name:              "APIGEE",
		ActiveEnvironment: "Local",
		Environments: []*bru.BrunoEnvironment{
			{
				Name: "Local",
				Variables: map[string]string{
					"FQDN": "http://localhost:8080",
					"Path": "api/v1",
				},
			},
		},
		CollectionHeaders: []bru.BruHeader{
			{Name: "X-Collection-Id", Value: "12345", Enabled: true},
		},
		Folders: []*bru.BrunoFolder{
			{
				ID:   "f1",
				Name: "Auth",
				Requests: []*bru.BrunoRequest{
					{
						ID:       "r1",
						Name:     "Login OTP",
						Method:   bru.MethodPost,
						URL:      "{{FQDN}}/{{Path}}/auth/otp",
						Headers:  []bru.BruHeader{{Name: "Content-Type", Value: "application/json", Enabled: true}},
						BodyType: "json",
						BodyContent: `{"phone": "123456789"}`,
					},
					{
						ID:     "r2",
						Name:   "Reset Password",
						Method: bru.MethodPut,
						URL:    "{{FQDN}}/{{Path}}/reset/password/send/otp",
					},
				},
			},
		},
	}

	sizes := [][2]int{
		{80, 24},
		{100, 30},
		{120, 35},
		{160, 45},
	}

	for _, size := range sizes {
		w := size[0]
		h := size[1]

		m := NewModel(coll)
		m.Width = w
		m.Height = h

		view := m.View()
		renderedW := lipgloss.Width(view)
		renderedH := lipgloss.Height(view)

		if renderedW != w {
			t.Errorf("At %dx%d, rendered width was %d", w, h, renderedW)
		}
		if renderedH != h {
			t.Errorf("At %dx%d, rendered height was %d", w, h, renderedH)
		}

		lines := strings.Split(view, "\n")
		if len(lines) != h {
			t.Errorf("At %dx%d, split lines count was %d", w, h, len(lines))
		}
		for lineIdx, line := range lines {
			lineWidth := lipgloss.Width(line)
			if lineWidth > w {
				t.Errorf("At %dx%d, line %d width %d exceeded %d: %q", w, h, lineIdx, lineWidth, w, line)
			}
		}
	}
}

func TestModelAutocomplete(t *testing.T) {
	coll := &bru.BrunoCollection{
		Name:              "TestColl",
		ActiveEnvironment: "Dev",
		Environments: []*bru.BrunoEnvironment{
			{
				Name: "Dev",
				Variables: map[string]string{
					"FQDN-Reset": "https://auth.example.com",
					"Path-Reset": "v2",
				},
			},
		},
	}
	m := NewModel(coll)
	m.IsEditingURL = true
	m.URLEditText = "{{FQ"
	m.URLCursor = len(m.URLEditText)
	m.updateAutocomplete()

	if len(m.ACCandidates) == 0 {
		t.Fatalf("Expected autocomplete candidates for '{{FQ', got none")
	}
	if m.ACCandidates[0].Name != "FQDN-Reset" {
		t.Errorf("Expected candidate FQDN-Reset, got %s", m.ACCandidates[0].Name)
	}

	// Test accept
	m.acceptAutocomplete()
	if m.URLEditText != "{{FQDN-Reset}}" {
		t.Errorf("Expected accepted URL to be '{{FQDN-Reset}}', got '%s'", m.URLEditText)
	}
}

func TestMouseInteractions(t *testing.T) {
	coll := &bru.BrunoCollection{
		Name:              "APIGEE",
		ActiveEnvironment: "Local",
		Environments: []*bru.BrunoEnvironment{
			{Name: "Local", Variables: map[string]string{"A": "1"}},
			{Name: "Staging", Variables: map[string]string{"A": "2"}},
		},
		Folders: []*bru.BrunoFolder{
			{
				ID:   "f1",
				Name: "Auth",
				Requests: []*bru.BrunoRequest{
					{ID: "r1", Name: "Login", Method: bru.MethodPost, URL: "http://api.com/login"},
					{ID: "r2", Name: "Reset", Method: bru.MethodPut, URL: "http://api.com/reset"},
				},
			},
		},
	}

	m := NewModel(coll)
	m.Width = 100
	m.Height = 30

	// 1. Click on folder to collapse
	folderClick := tea.MouseMsg{
		X:      5,
		Y:      6, // Line 6 is folder row
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	newM, _ := m.Update(folderClick)
	m = newM.(Model)
	if m.ExpandedFolders["f1"] {
		t.Errorf("Expected folder f1 to be collapsed after click, but was expanded")
	}

	// Click again to expand
	newM, _ = m.Update(folderClick)
	m = newM.(Model)
	if !m.ExpandedFolders["f1"] {
		t.Errorf("Expected folder f1 to be expanded after second click")
	}

	// 2. Click on second request (row 2 -> Y=8)
	req2Click := tea.MouseMsg{
		X:      5,
		Y:      8,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	newM, _ = m.Update(req2Click)
	m = newM.(Model)
	if m.SelectedRequest == nil || m.SelectedRequest.ID != "r2" {
		t.Errorf("Expected selected request to be r2, got %v", m.SelectedRequest)
	}

	// 3. Click on Method badge in Request pane (X: sidebarW+2 = 32, Y: 6)
	methodClick := tea.MouseMsg{
		X:      32,
		Y:      6,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	curMethod := m.SelectedRequest.Method
	newM, _ = m.Update(methodClick)
	m = newM.(Model)
	if m.SelectedRequest.Method == curMethod {
		t.Errorf("Expected method to cycle after clicking method badge")
	}

	// 4. Click on URL to edit (X: 45, Y: 6)
	urlClick := tea.MouseMsg{
		X:      45,
		Y:      6,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	newM, _ = m.Update(urlClick)
	m = newM.(Model)
	if !m.IsEditingURL {
		t.Errorf("Expected IsEditingURL to be true after clicking URL")
	}

	// 5. Click on Tab 3 (Body) (X: sidebarW + 35 = 65, Y: 7)
	tabClick := tea.MouseMsg{
		X:      65,
		Y:      7,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	newM, _ = m.Update(tabClick)
	m = newM.(Model)
	if m.ActiveTab != "body" {
		t.Errorf("Expected ActiveTab to be 'body' after click, got '%s'", m.ActiveTab)
	}

	// 6. Mouse wheel scroll on sidebar
	sidebarWheelDown := tea.MouseMsg{
		X:      10,
		Y:      10,
		Button: tea.MouseButtonWheelDown,
	}
	newM, _ = m.Update(sidebarWheelDown)
	m = newM.(Model)

	// 7. Click on Env button in status bar (X: 35, Y: 29) -> Opens Environment Modal
	envClick := tea.MouseMsg{
		X:      35,
		Y:      29,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	newM, _ = m.Update(envClick)
	m = newM.(Model)
	if !m.IsEnvModalOpen {
		t.Errorf("Expected IsEnvModalOpen to be true after clicking Env in status bar")
	}

	// Click on second environment inside modal to activate it
	modalClick := tea.MouseMsg{
		X:      20,
		Y:      8, // Row inside modal environment list
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	newM, _ = m.Update(modalClick)
	m = newM.(Model)
	if m.IsEnvModalOpen {
		t.Errorf("Expected modal to close after selecting environment")
	}
}

func TestSidebarLongNameNoWrap(t *testing.T) {
	coll := &bru.BrunoCollection{
		Name: "TestLongNames",
		Requests: []*bru.BrunoRequest{
			{
				ID:     "r1",
				Name:   "Reset Password - Set New Password (Very Long Name That Exceeds Width)",
				Method: bru.MethodPut,
			},
		},
	}
	m := NewModel(coll)
	m.Width = 80
	m.Height = 24

	view := m.renderSidebar(24, 18)
	lines := strings.Split(view, "\n")
	if len(lines) != 18 {
		t.Fatalf("Expected sidebar height to be 18 lines, got %d", len(lines))
	}
	for i, l := range lines {
		t.Logf("Line %d: %q", i, l)
		w := lipgloss.Width(l)
		if w != 24 {
			t.Errorf("Line %d width was %d, expected 24: %q", i, w, l)
		}
	}
}

func TestEnvironmentModal(t *testing.T) {
	coll := &bru.BrunoCollection{
		Name:              "APIGEE",
		ActiveEnvironment: "SIT",
		Environments: []*bru.BrunoEnvironment{
			{Name: "EVO", Variables: map[string]string{"HOST": "evo.tim.it"}},
			{Name: "GREEN", Variables: map[string]string{"HOST": "green.tim.it"}},
			{Name: "SIT", Variables: map[string]string{"HOST": "sit.tim.it", "TOKEN": "secret123"}},
		},
	}
	m := NewModel(coll)
	m.Width = 100
	m.Height = 30
	m.IsEnvModalOpen = true
	m.EnvModalSelectedIdx = 2 // SIT

	view := m.View()
	w := lipgloss.Width(view)
	h := lipgloss.Height(view)

	if w != 100 {
		t.Errorf("Expected modal view width 100, got %d", w)
	}
	if h != 30 {
		t.Errorf("Expected modal view height 30, got %d", h)
	}

	if !strings.Contains(view, "ENVIRONMENT & VARIABLES MANAGER") {
		t.Errorf("Expected view to contain modal title")
	}
	if !strings.Contains(view, "SIT") {
		t.Errorf("Expected view to contain SIT environment")
	}
}

func TestOverlayModalPreservesBackground(t *testing.T) {
	bgLine := "│ POST Reset Password - Send OTP │ Right Pane Content Here! │"
	modalLine := "╭── MODAL ──╮"
	// screenW = 60
	// startCol = 10, modalW = 13
	screenW := ansi.StringWidth(bgLine)
	startCol := 10
	modalW := ansi.StringWidth(modalLine)

	left := ansi.Truncate(bgLine, startCol, "")
	right := ansi.TruncateLeft(bgLine, startCol+modalW, "")
	combined := left + modalLine + right

	if ansi.StringWidth(combined) != screenW {
		t.Fatalf("Expected combined width %d, got %d", screenW, ansi.StringWidth(combined))
	}
	if !strings.HasPrefix(combined, "│ POST Res") {
		t.Fatalf("Expected left to be preserved, got %q", combined)
	}
	if !strings.HasSuffix(combined, "Content Here! │") {
		t.Fatalf("Expected right to be preserved, got %q", combined)
	}
}

func TestRenderHeaderLines(t *testing.T) {
	coll := &bru.BrunoCollection{Name: "APIGEE"}
	m := NewModel(coll)
	header := m.renderHeader(100, 5)
	lines := strings.Split(header, "\n")
	t.Logf("Header lines count: %d", len(lines))
	for i, l := range lines {
		t.Logf("Header line %d: %q", i, l)
	}
}

func TestLipglossBorderHeightMath(t *testing.T) {
	s1 := lipgloss.NewStyle().
		Width(50).
		Height(3).
		BorderStyle(lipgloss.RoundedBorder())
	out1 := s1.Render("line 1\nline 2\nline 3")
	lines1 := strings.Split(out1, "\n")
	t.Logf("Height(3) with border: %d lines", len(lines1))
	for i, l := range lines1 {
		t.Logf("s1 line %d: %q", i, l)
	}

	s2 := lipgloss.NewStyle().
		Width(50).
		Height(5).
		BorderStyle(lipgloss.RoundedBorder())
	out2 := s2.Render("line 1\nline 2\nline 3")
	lines2 := strings.Split(out2, "\n")
	t.Logf("Height(5) with border: %d lines", len(lines2))
	for i, l := range lines2 {
		t.Logf("s2 line %d: %q", i, l)
	}
}

func TestBoxOuterDimensionsWithBorders(t *testing.T) {
	W := 60
	H := 10

	// We want the box to be EXACTLY W x H on screen, with top/bottom/left/right rounded border
	s := lipgloss.NewStyle().
		Width(W - 2).
		Height(H - 2).
		MaxWidth(W).
		MaxHeight(H).
		BorderStyle(lipgloss.RoundedBorder())

	out := s.Render("hello\nworld")
	lines := strings.Split(out, "\n")

	t.Logf("Total lines: %d (expected %d)", len(lines), H)
	if len(lines) != H {
		t.Fatalf("Expected %d lines, got %d", H, len(lines))
	}
	t.Logf("Line 0 (top border): %q", lines[0])
	t.Logf("Line %d (bottom border): %q", H-1, lines[H-1])

	if !strings.HasPrefix(lines[0], "╭") || !strings.HasSuffix(lines[0], "╮") {
		t.Errorf("Line 0 is not top border: %q", lines[0])
	}
	if !strings.HasPrefix(lines[H-1], "╰") || !strings.HasSuffix(lines[H-1], "╯") {
		t.Errorf("Line %d is not bottom border: %q", H-1, lines[H-1])
	}

	for i, l := range lines {
		w := ansi.StringWidth(l)
		if w != W {
			t.Errorf("Line %d width is %d, expected %d: %q", i, w, W, l)
		}
	}
}

func TestTabContentEditingHeadersAndBody(t *testing.T) {
	req := &bru.BrunoRequest{
		ID:       "req1",
		Name:     "Test Request",
		Method:   bru.MethodGet,
		URL:      "https://example.com/api",
		BodyType: "json",
		Headers: []bru.BruHeader{
			{Name: "Accept", Value: "application/json", Enabled: true},
		},
		BodyContent: `{"foo": "bar"}`,
	}
	coll := &bru.BrunoCollection{
		Name:     "TestColl",
		Requests: []*bru.BrunoRequest{req},
	}
	m := NewModel(coll)
	m.Width = 100
	m.Height = 30
	m.ActiveTab = "headers"

	// 1. Enter editing mode with 'I'
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = newM.(Model)
	if !m.IsEditingContent {
		t.Fatalf("Expected IsEditingContent to be true after pressing 'i'")
	}
	if !strings.Contains(m.ContentEditText, "Accept: application/json") {
		t.Fatalf("Expected ContentEditText to have header line, got %q", m.ContentEditText)
	}

	// 2. Add a new header line
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(Model)
	for _, ch := range "X-Custom: 123" {
		newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = newM.(Model)
	}

	// 3. Exit edit mode with Esc
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = newM.(Model)
	if m.IsEditingContent {
		t.Fatalf("Expected IsEditingContent to be false after Esc")
	}
	if len(req.Headers) != 2 {
		t.Fatalf("Expected 2 headers, got %d", len(req.Headers))
	}
	if req.Headers[1].Name != "X-Custom" || req.Headers[1].Value != "123" {
		t.Errorf("Header 1 mismatch: %+v", req.Headers[1])
	}

	// 4. Test Body editing
	m.ActiveTab = "body"
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = newM.(Model)
	if !m.IsEditingContent {
		t.Fatalf("Expected IsEditingContent to be true for body")
	}
	// Append extra JSON field
	for _, ch := range ` // modified` {
		newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = newM.(Model)
	}
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = newM.(Model)
	if !strings.Contains(req.BodyContent, "// modified") {
		t.Errorf("Expected body to be modified, got %q", req.BodyContent)
	}
}

func TestCreateNewRequest(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "brui-test-create-req-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	coll := &bru.BrunoCollection{
		Name:     "TestColl",
		Path:     tempDir,
		Requests: []*bru.BrunoRequest{},
	}
	m := NewModel(coll)
	m.Width = 100
	m.Height = 30

	// 1. Open New Request modal with 'N'
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = newM.(Model)
	if !m.IsNewReqPromptOpen {
		t.Fatalf("Expected IsNewReqPromptOpen to be true")
	}
	if m.NewReqStep != 0 {
		t.Fatalf("Expected step 0, got %d", m.NewReqStep)
	}

	// Type request name: "User Details"
	for _, ch := range "User Details" {
		newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = newM.(Model)
	}
	if m.NewReqName != "User Details" {
		t.Errorf("Expected name 'User Details', got %q", m.NewReqName)
	}

	// Tab to Step 1 (Method)
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = newM.(Model)
	if m.NewReqStep != 1 {
		t.Fatalf("Expected step 1, got %d", m.NewReqStep)
	}

	// Press right arrow to cycle to POST
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = newM.(Model)
	if m.NewReqMethod != bru.MethodPost {
		t.Errorf("Expected POST method, got %s", m.NewReqMethod)
	}

	// Tab to Step 2 (URL)
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = newM.(Model)
	if m.NewReqStep != 2 {
		t.Fatalf("Expected step 2, got %d", m.NewReqStep)
	}

	// Type URL
	for _, ch := range "api/users" {
		newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = newM.(Model)
	}

	// Enter on step 2 submits and creates request
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(Model)
	if m.IsNewReqPromptOpen {
		t.Fatalf("Expected prompt to close after creation")
	}

	if len(coll.Requests) != 1 {
		t.Fatalf("Expected 1 request in collection, got %d", len(coll.Requests))
	}
	created := coll.Requests[0]
	if created.Name != "User Details" || created.Method != bru.MethodPost {
		t.Errorf("Created request mismatch: %+v", created)
	}
	if _, err := os.Stat(created.FilePath); os.IsNotExist(err) {
		t.Errorf("Expected request file %s to exist on disk", created.FilePath)
	}
}

func TestEnvironmentModalVisual(t *testing.T) {
	coll := &bru.BrunoCollection{
		Name: "APIGEE",
		Environments: []*bru.BrunoEnvironment{
			{Name: "EVO", Variables: map[string]string{"FQDN": "https://api.test", "KEY": "val"}},
			{Name: "GREEN", Variables: map[string]string{"FQDN": "https://api.prod"}},
		},
		ActiveEnvironment: "EVO",
	}
	m := NewModel(coll)
	m.Width = 100
	m.Height = 24
	m.IsEnvModalOpen = true

	out := m.View()
	lines := strings.Split(out, "\n")
	t.Logf("Total rendered lines: %d (expected %d)", len(lines), m.Height)
	for i, l := range lines {
		w := ansi.StringWidth(l)
		t.Logf("Line %2d (width=%d): %s", i, w, l)
		if w != m.Width {
			t.Errorf("Line %d width = %d, expected %d", i, w, m.Width)
		}
	}
}

func TestEnvironmentAndVariableEditing(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "brui-test-env-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	env1 := &bru.BrunoEnvironment{
		Name: "DEV",
		Variables: map[string]string{
			"BASE_URL": "http://dev.example.com",
			"TOKEN":    "secret123",
		},
	}
	coll := &bru.BrunoCollection{
		Name:              "TestColl",
		Path:              tempDir,
		Environments:      []*bru.BrunoEnvironment{env1},
		ActiveEnvironment: "DEV",
	}
	m := NewModel(coll)
	m.Width = 100
	m.Height = 30

	// 1. Open Env Modal with 'E'
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m = newM.(Model)
	if !m.IsEnvModalOpen {
		t.Fatalf("Expected IsEnvModalOpen to be true")
	}
	if m.EnvModalFocusPane != "envs" {
		t.Fatalf("Expected focus on envs")
	}

	// 2. Tab to switch focus to variables
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = newM.(Model)
	if m.EnvModalFocusPane != "vars" {
		t.Fatalf("Expected focus on vars")
	}

	// 3. Press Enter to edit the first variable
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(Model)
	if !m.IsEnvPromptOpen {
		t.Fatalf("Expected IsEnvPromptOpen to be true")
	}
	if m.EnvPromptTempKey != "BASE_URL" {
		t.Fatalf("Expected variable BASE_URL, got %q", m.EnvPromptTempKey)
	}

	// Modify value: add "/v2"
	for _, ch := range "/v2" {
		newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = newM.(Model)
	}
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(Model)
	if m.IsEnvPromptOpen {
		t.Fatalf("Expected prompt to close")
	}
	if env1.Variables["BASE_URL"] != "http://dev.example.com/v2" {
		t.Errorf("Expected updated BASE_URL, got %q", env1.Variables["BASE_URL"])
	}

	// 4. Add new variable with 'N' while focused on vars
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = newM.(Model)
	if !m.IsEnvPromptOpen || m.EnvPromptAction != "new_var_name" {
		t.Fatalf("Expected prompt for new_var_name")
	}
	for _, ch := range "NEW_KEY" {
		newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = newM.(Model)
	}
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(Model)
	if !m.IsEnvPromptOpen || m.EnvPromptAction != "new_var_val" {
		t.Fatalf("Expected prompt for new_var_val")
	}
	for _, ch := range "new_value" {
		newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = newM.(Model)
	}
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(Model)
	if env1.Variables["NEW_KEY"] != "new_value" {
		t.Errorf("Expected NEW_KEY in env, got %v", env1.Variables)
	}

	// 5. Delete variable with 'D'
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = newM.(Model)
	if len(env1.Variables) != 2 {
		t.Errorf("Expected 2 variables after delete, got %d", len(env1.Variables))
	}
}

func TestThemeCycling(t *testing.T) {
	coll := &bru.BrunoCollection{Name: "TestColl"}
	m := NewModel(coll)
	m.Width = 100
	m.Height = 30

	if m.Theme.Name != "Catppuccin Mocha" {
		t.Errorf("Expected initial theme Mocha, got %s", m.Theme.Name)
	}

	// Press 't' to cycle theme
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	m = newM.(Model)
	if m.Theme.Name != "Catppuccin Macchiato" {
		t.Errorf("Expected Macchiato, got %s", m.Theme.Name)
	}

	// Press 't' again -> Frappe
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	m = newM.(Model)
	if m.Theme.Name != "Catppuccin Frappé" {
		t.Errorf("Expected Frappe, got %s", m.Theme.Name)
	}

	// Press 't' again -> Latte
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	m = newM.(Model)
	if m.Theme.Name != "Catppuccin Latte" {
		t.Errorf("Expected Latte, got %s", m.Theme.Name)
	}

	// Press 't' again -> Tokyo Night
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	m = newM.(Model)
	if m.Theme.Name != "Tokyo Night" {
		t.Errorf("Expected Tokyo Night, got %s", m.Theme.Name)
	}

	// Press 't' again -> Dracula
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	m = newM.(Model)
	if m.Theme.Name != "Dracula" {
		t.Errorf("Expected Dracula, got %s", m.Theme.Name)
	}

	// Check modal in Dracula theme
	m.IsEnvModalOpen = true
	modalView := m.View()
	if !strings.Contains(modalView, "ENVIRONMENT & VARIABLES MANAGER") {
		t.Errorf("Expected modal title in Dracula theme")
	}

	// Click on [T] Theme in status bar (X: 72, Y: 29) -> cycles back to Mocha
	m.IsEnvModalOpen = false
	themeClick := tea.MouseMsg{
		X:      72,
		Y:      29,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}
	newM, _ = m.Update(themeClick)
	m = newM.(Model)
	if m.Theme.Name != "Catppuccin Mocha" {
		t.Errorf("Expected cycle back to Mocha via mouse click, got %s", m.Theme.Name)
	}
}

func TestHeadersTabCleanDisplayAndEdit(t *testing.T) {
	req := &bru.BrunoRequest{
		ID:     "req1",
		Name:   "Test Request",
		Method: bru.MethodPost,
		URL:    "https://example.com/api/test",
		Headers: []bru.BruHeader{
			{Name: "Content-Type", Value: "application/json", Enabled: true},
		},
	}
	coll := &bru.BrunoCollection{
		Name: "TestColl",
		CollectionHeaders: []bru.BruHeader{
			{Name: "Accept-Encoding", Value: "gzip,deflate", Enabled: true},
			{Name: "sourceSystem", Value: "CBE", Enabled: true},
		},
		Requests: []*bru.BrunoRequest{req},
	}

	m := NewModel(coll)
	m.Width = 100
	m.Height = 30
	m.ActiveTab = "headers"

	// View should NOT show duplicated/cluttered Inherited from collection.bru section
	view := m.View()
	if strings.Contains(view, "Inherited from collection.bru") {
		t.Fatalf("Expected view NOT to show Inherited from collection.bru")
	}
	if !strings.Contains(view, "Content-Type") {
		t.Fatalf("Expected view to show request Content-Type header")
	}

	// Press 'i' to start editing -> should only edit request headers cleanly
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = newM.(Model)
	if !m.IsEditingContent {
		t.Fatalf("Expected edit mode to be active")
	}
	if strings.Contains(m.ContentEditText, "Inherited from collection.bru") {
		t.Fatalf("Expected ContentEditText NOT to have collection headers banner, got %q", m.ContentEditText)
	}
}

func TestRequestPaneWheelScrollingAndProtection(t *testing.T) {
	req := &bru.BrunoRequest{
		ID:     "req1",
		Name:   "Test Request",
		Method: bru.MethodGet,
		URL:    "https://example.com/api/test",
		Headers: []bru.BruHeader{
			{Name: "H1", Value: "V1", Enabled: true},
			{Name: "H2", Value: "V2", Enabled: true},
			{Name: "H3", Value: "V3", Enabled: true},
			{Name: "H4", Value: "V4", Enabled: true},
			{Name: "H5", Value: "V5", Enabled: true},
			{Name: "H6", Value: "V6", Enabled: true},
			{Name: "H7", Value: "V7", Enabled: true},
			{Name: "H8", Value: "V8", Enabled: true},
			{Name: "H9", Value: "V9", Enabled: true},
			{Name: "H10", Value: "V10", Enabled: true},
		},
	}
	coll := &bru.BrunoCollection{
		Name:     "TestColl",
		Requests: []*bru.BrunoRequest{req},
	}

	m := NewModel(coll)
	m.Width = 100
	m.Height = 25
	sidebarW, _, _, _, _ := m.layoutDimensions()

	// Mouse wheel down over Request Pane (X: sidebarW+5, Y: 8)
	wheelDown := tea.MouseMsg{
		X:      sidebarW + 5,
		Y:      8,
		Button: tea.MouseButtonWheelDown,
	}
	newM, _ := m.Update(wheelDown)
	m = newM.(Model)
	if m.ReqScroll != 1 {
		t.Errorf("Expected ReqScroll=1 after wheel down, got %d", m.ReqScroll)
	}
	if m.IsEditingURL {
		t.Errorf("Mouse wheel down must NEVER trigger URL editing")
	}

	// Scrolling over row 6 (Method / URL row) with wheel must NOT trigger edit or cycle method
	wheelOverRow6 := tea.MouseMsg{
		X:      sidebarW + 5,
		Y:      6,
		Button: tea.MouseButtonWheelDown,
	}
	newM, _ = m.Update(wheelOverRow6)
	m = newM.(Model)
	if m.ReqScroll != 2 {
		t.Errorf("Expected ReqScroll=2 after wheel over row 6, got %d", m.ReqScroll)
	}
	if m.IsEditingURL {
		t.Errorf("Wheel over row 6 must not activate URL editing")
	}
	if m.SelectedRequest.Method != bru.MethodGet {
		t.Errorf("Wheel over row 6 must not change method, got %s", m.SelectedRequest.Method)
	}
}


