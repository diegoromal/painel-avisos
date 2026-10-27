package main

import (
	"html"
	"regexp"
	"strings"
)

// Simple, safe formatting for announcement text (input is always escaped first):
//
//	**negrito**   *itálico*   __sublinhado__   linhas iniciadas por "- " viram lista
//	linha em branco = novo parágrafo
var (
	reBold      = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reUnderline = regexp.MustCompile(`__(.+?)__`)
	reItalic    = regexp.MustCompile(`\*(.+?)\*`)
)

func inline(s string) string {
	s = html.EscapeString(s)
	s = reBold.ReplaceAllString(s, "<strong>$1</strong>")
	s = reUnderline.ReplaceAllString(s, "<u>$1</u>")
	s = reItalic.ReplaceAllString(s, "<em>$1</em>")
	return s
}

func renderText(src string) string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	var out strings.Builder
	var para []string
	inList := false
	flushPara := func() {
		if len(para) > 0 {
			out.WriteString("<p>" + strings.Join(para, "<br>") + "</p>")
			para = nil
		}
	}
	closeList := func() {
		if inList {
			out.WriteString("</ul>")
			inList = false
		}
	}
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "":
			flushPara()
			closeList()
		case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "• "):
			flushPara()
			if !inList {
				out.WriteString("<ul>")
				inList = true
			}
			item := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, "- "), "• "))
			out.WriteString("<li>" + inline(item) + "</li>")
		default:
			closeList()
			para = append(para, inline(t))
		}
	}
	flushPara()
	closeList()
	return out.String()
}
