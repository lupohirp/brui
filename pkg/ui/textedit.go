package ui

import "unicode/utf8"

// Cursor positions in the inline editors are byte offsets into UTF-8 text.
// These helpers keep them on rune boundaries so multi-byte characters
// (accents, symbols, emoji) are moved over and deleted as a whole.

// prevBoundary returns the offset of the rune that ends at i.
func prevBoundary(s string, i int) int {
	if i <= 0 {
		return 0
	}
	if i > len(s) {
		i = len(s)
	}
	_, n := utf8.DecodeLastRuneInString(s[:i])
	return i - n
}

// nextBoundary returns the offset just past the rune that starts at i.
func nextBoundary(s string, i int) int {
	if i >= len(s) {
		return len(s)
	}
	if i < 0 {
		i = 0
	}
	_, n := utf8.DecodeRuneInString(s[i:])
	return i + n
}

// deleteBefore removes the rune before the cursor (Backspace).
func deleteBefore(s string, cursor int) (string, int) {
	if cursor <= 0 {
		return s, 0
	}
	p := prevBoundary(s, cursor)
	return s[:p] + s[cursor:], p
}

// deleteAfter removes the rune at the cursor (Delete).
func deleteAfter(s string, cursor int) string {
	if cursor >= len(s) {
		return s
	}
	return s[:cursor] + s[nextBoundary(s, cursor):]
}

// runeOffset converts a column counted in runes to a byte offset in line,
// clamped to the line length.
func runeOffset(line string, col int) int {
	off := 0
	for i := 0; i < col && off < len(line); i++ {
		off = nextBoundary(line, off)
	}
	return off
}

// runeCol returns the rune column of byte offset off within line.
func runeCol(line string, off int) int {
	if off > len(line) {
		off = len(line)
	}
	return utf8.RuneCountInString(line[:off])
}

// splitAtCursor returns the text before the cursor, the rune under it (" " at
// end of text) and the text after it, for rendering a block cursor.
func splitAtCursor(s string, cursor int) (before, at, after string) {
	if cursor >= len(s) {
		return s, " ", ""
	}
	n := nextBoundary(s, cursor)
	return s[:cursor], s[cursor:n], s[n:]
}
