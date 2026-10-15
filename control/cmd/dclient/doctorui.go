package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"

	statusWidth   = 6
	minDetail     = 24
	fallbackWidth = 120
)

type doctorTable struct {
	w      io.Writer
	color  bool
	name   int
	detail int
}

func newDoctorTable(f *os.File, nameWidth int) *doctorTable {
	width := fallbackWidth
	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	tty := err == nil && ws.Col > 0
	if tty {
		width = int(ws.Col)
	}
	return &doctorTable{
		w:      f,
		color:  tty && os.Getenv("NO_COLOR") == "",
		name:   nameWidth,
		detail: max(width-statusWidth-nameWidth-10, minDetail),
	}
}

func (t *doctorTable) paint(code, s string) string {
	if !t.color || code == "" {
		return s
	}
	return code + s + ansiReset
}

func (t *doctorTable) border(left, mid, right string) {
	line := left + strings.Repeat("─", statusWidth+2) + mid +
		strings.Repeat("─", t.name+2) + mid + strings.Repeat("─", t.detail+2) + right
	fmt.Fprintln(t.w, t.paint(ansiDim, line))
}

func (t *doctorTable) line(status, name, detail string, sc, nc, dc string) {
	bar := t.paint(ansiDim, "│")
	fmt.Fprintf(t.w, "%s %s %s %s %s %s %s\n", bar,
		t.paint(sc, pad(status, statusWidth)), bar,
		t.paint(nc, pad(name, t.name)), bar,
		t.paint(dc, pad(detail, t.detail)), bar)
}

func (t *doctorTable) header() {
	t.border("╭", "┬", "╮")
	t.line("", "check", "detail", "", ansiBold, ansiBold)
	t.border("├", "┼", "┤")
}

func (t *doctorTable) row(res result, name, detail string) {
	icon, color, dc := "✓", ansiGreen, ansiDim
	switch res {
	case warn:
		icon, color, dc = "!", ansiYellow, ansiYellow
	case fail:
		icon, color, dc = "✗", ansiRed, ansiRed
	}
	for i, chunk := range wrap(detail, t.detail) {
		if i == 0 {
			t.line(icon+" "+res.String(), name, chunk, ansiBold+color, "", dc)
			continue
		}
		t.line("", "", chunk, "", "", dc)
	}
}

func (t *doctorTable) footer(counts map[result]int) {
	t.border("╰", "┴", "╯")
	parts := []string{t.paint(ansiGreen, fmt.Sprintf("%d passed", counts[pass]))}
	if n := counts[warn]; n > 0 {
		parts = append(parts, t.paint(ansiYellow, fmt.Sprintf("%d warning(s)", n)))
	}
	if n := counts[fail]; n > 0 {
		parts = append(parts, t.paint(ansiBold+ansiRed, fmt.Sprintf("%d failed", n)))
	}
	fmt.Fprintln(t.w, strings.Join(parts, t.paint(ansiDim, " · ")))
}

func pad(s string, width int) string {
	return s + strings.Repeat(" ", max(width-utf8.RuneCountInString(s), 0))
}

func wrap(s string, width int) []string {
	var lines []string
	var cur []rune
	for _, word := range strings.Fields(s) {
		w := []rune(word)
		if len(cur) > 0 && len(cur)+1+len(w) > width {
			lines = append(lines, string(cur))
			cur = nil
		}
		if len(cur) > 0 {
			cur = append(cur, ' ')
		}
		cur = append(cur, w...)
		for len(cur) > width {
			lines = append(lines, string(cur[:width]))
			cur = cur[width:]
		}
	}
	if len(cur) > 0 || len(lines) == 0 {
		lines = append(lines, string(cur))
	}
	return lines
}
