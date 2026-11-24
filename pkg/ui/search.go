package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lupohirp/brui/pkg/bru"
)

// Explorer search ([/] or Ctrl+F): filters the tree while typing; Enter
// opens the selected result, Esc restores the previous selection.

type searchState struct {
	Query   string
	prevIdx int
	prevReq *bru.BrunoRequest
}

func (m *Model) openSearch() {
	if m.Search != nil {
		return
	}
	m.Search = &searchState{prevIdx: m.SelectedIdx, prevReq: m.SelectedRequest}
	m.StatusMsg = "Search: type to filter   [↑/↓] Select   [Enter] Open   [Esc] Cancel"
	m.rebuildTree()
}

// searchMatches reports whether a request matches every term of the query
// (case-insensitive) in its folder path, name, method or URL.
func searchMatches(terms []string, folderPath string, r *bru.BrunoRequest) bool {
	hay := strings.ToLower(folderPath + " " + r.Name + " " + string(r.Method) + " " + r.URL)
	for _, t := range terms {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

// searchTree builds the filtered tree: matching requests with the folders
// that contain them, all shown open.
func (m *Model) searchTree(query string) []TreeItem {
	terms := strings.Fields(strings.ToLower(query))
	var items []TreeItem
	for _, r := range m.Coll.Requests {
		if searchMatches(terms, "", r) {
			items = append(items, TreeItem{Type: "request", Request: r})
		}
	}
	var walk func(f *bru.BrunoFolder, path string, depth int) []TreeItem
	walk = func(f *bru.BrunoFolder, path string, depth int) []TreeItem {
		path += "/" + f.Name
		var sub []TreeItem
		for _, sf := range f.Folders {
			sub = append(sub, walk(sf, path, depth+1)...)
		}
		for _, r := range f.Requests {
			if searchMatches(terms, path, r) {
				sub = append(sub, TreeItem{Type: "request", Request: r, Depth: depth + 1})
			}
		}
		if len(sub) == 0 {
			return nil
		}
		return append([]TreeItem{{Type: "folder", Folder: f, Depth: depth, Expanded: true}}, sub...)
	}
	for _, f := range m.Coll.Folders {
		items = append(items, walk(f, "", 0)...)
	}
	return items
}

// selectFirstResult moves the selection to the first matching request.
func (m *Model) selectFirstResult() {
	for i, it := range m.VisibleItems {
		if it.Type == "request" {
			m.SelectedIdx = i
			m.selectCurrentItem()
			return
		}
	}
	m.SelectedIdx = 0
}

func (m *Model) searchResultCount() int {
	n := 0
	for _, it := range m.VisibleItems {
		if it.Type == "request" {
			n++
		}
	}
	return n
}

// folderChain returns the IDs of the folders containing the request.
func folderChain(folders []*bru.BrunoFolder, target *bru.BrunoRequest) []string {
	for _, f := range folders {
		for _, r := range f.Requests {
			if r == target {
				return []string{f.ID}
			}
		}
		if sub := folderChain(f.Folders, target); sub != nil {
			return append([]string{f.ID}, sub...)
		}
	}
	return nil
}

// acceptSearch closes the search keeping the selected item, with its
// folders opened so it stays visible in the normal tree.
func (m *Model) acceptSearch() {
	var picked TreeItem
	ok := m.SelectedIdx >= 0 && m.SelectedIdx < len(m.VisibleItems)
	if ok {
		picked = m.VisibleItems[m.SelectedIdx]
	}
	m.Search = nil
	if !ok {
		m.rebuildTree()
		m.StatusMsg = "No results"
		return
	}
	if picked.Type == "request" {
		for _, id := range folderChain(m.Coll.Folders, picked.Request) {
			m.ExpandedFolders[id] = true
		}
	} else {
		m.ExpandedFolders[picked.Folder.ID] = true
	}
	m.rebuildTree()
	for i, it := range m.VisibleItems {
		if (picked.Type == "request" && it.Request == picked.Request) || (picked.Type == "folder" && it.Folder == picked.Folder) {
			m.SelectedIdx = i
			break
		}
	}
	m.selectCurrentItem()
	m.StatusMsg = "Ready"
}

func (m *Model) cancelSearch() {
	st := m.Search
	m.Search = nil
	m.rebuildTree()
	m.SelectedIdx = min(st.prevIdx, max(0, len(m.VisibleItems)-1))
	m.SelectedRequest = st.prevReq
	m.syncSidebarOffset()
	m.StatusMsg = "Ready"
}

func (m *Model) handleSearchKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	st := m.Search
	switch msg.Type {
	case tea.KeyEsc:
		m.cancelSearch()
		return *m, nil
	case tea.KeyEnter:
		m.acceptSearch()
		return *m, nil
	case tea.KeyUp, tea.KeyCtrlP:
		if m.SelectedIdx > 0 {
			m.SelectedIdx--
			m.selectCurrentItem()
		}
		return *m, nil
	case tea.KeyDown, tea.KeyCtrlN:
		if m.SelectedIdx < len(m.VisibleItems)-1 {
			m.SelectedIdx++
			m.selectCurrentItem()
		}
		return *m, nil
	case tea.KeyBackspace:
		if st.Query != "" {
			_, size := lastRune(st.Query)
			st.Query = st.Query[:len(st.Query)-size]
		}
	case tea.KeyCtrlU:
		st.Query = ""
	case tea.KeyCtrlW:
		q := strings.TrimRight(st.Query, " ")
		st.Query = q[:strings.LastIndex(q, " ")+1]
	case tea.KeyRunes, tea.KeySpace:
		ch := string(msg.Runes)
		if msg.Type == tea.KeySpace {
			ch = " "
		}
		st.Query += strings.NewReplacer("\n", " ", "\r", "").Replace(ch)
	default:
		return *m, nil
	}
	m.rebuildTree()
	m.selectFirstResult()
	return *m, nil
}

func lastRune(s string) (rune, int) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i]&0xC0 != 0x80 {
			return []rune(s[i:])[0], len(s) - i
		}
	}
	return 0, 0
}

// sidebarListHeight is the number of tree rows in the sidebar.
func (m Model) sidebarListHeight() int {
	_, _, bodyH, _, _ := m.layoutDimensions()
	h := bodyH - 2
	if m.Search != nil {
		h-- // search box row
	}
	return max(h, 1)
}

// sidebarViewOffset is the first visible tree row: SidebarOffset adjusted
// so the selection is visible. Rendering and mouse hit-testing both use it.
func (m Model) sidebarViewOffset() int {
	h := m.sidebarListHeight()
	offset := m.SidebarOffset
	if m.SelectedIdx < offset {
		offset = m.SelectedIdx
	} else if m.SelectedIdx >= offset+h {
		offset = m.SelectedIdx - h + 1
	}
	if maxOff := len(m.VisibleItems) - h; offset > maxOff {
		offset = maxOff
	}
	return max(offset, 0)
}

func (m *Model) syncSidebarOffset() { m.SidebarOffset = m.sidebarViewOffset() }

// searchBoxLine renders the search input shown at the top of the sidebar.
func (m Model) searchBoxLine(width int) string {
	t := m.Theme
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent)).Bold(true)
	sub := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Subtext))
	cursor := lipgloss.NewStyle().Foreground(lipgloss.Color("#11111b")).Background(lipgloss.Color(t.Accent)).Render(" ")
	count := sub.Render(fmt.Sprintf(" %d", m.searchResultCount()))
	if m.Search.Query == "" {
		count = sub.Render(" name, folder, URL")
	}
	query := m.Search.Query
	room := width - 4 - ansi.StringWidth(count)
	if ansi.StringWidth(query) > room && room > 1 {
		query = ansi.TruncateLeft(query, ansi.StringWidth(query)-room+1, "…")
	}
	line := " " + accent.Render("/") + " " + query + cursor + count
	return padAndPaint(line, width, t.Fg, t.Surface)
}
