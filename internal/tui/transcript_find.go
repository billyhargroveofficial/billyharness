package tui

import (
	"fmt"
	"regexp"
	"strings"
)

func (m *Model) applyFindQuery(query string) (int, error) {
	query = strings.TrimSpace(query)
	m.findQuery = query
	m.findMatchIndex = 0
	if query == "" {
		m.findMatches = nil
		m.viewport.ClearHighlights()
		return 0, nil
	}
	matches, err := findTranscriptMatches(m.viewportContent, query)
	if err != nil {
		m.findMatches = nil
		m.viewport.ClearHighlights()
		return 0, err
	}
	m.findMatches = matches
	m.setFindHighlights(true)
	return len(matches), nil
}

func (m *Model) reapplyFindQuery() {
	if strings.TrimSpace(m.findQuery) == "" {
		return
	}
	matches, err := findTranscriptMatches(m.viewportContent, m.findQuery)
	if err != nil {
		m.findMatches = nil
		m.viewport.ClearHighlights()
		return
	}
	m.findMatches = matches
	if len(m.findMatches) == 0 {
		m.findMatchIndex = 0
	} else if m.findMatchIndex >= len(m.findMatches) {
		m.findMatchIndex = 0
	}
	m.setFindHighlights(false)
}

func (m *Model) setFindHighlights(jumpTop bool) {
	m.viewport.ClearHighlights()
	if len(m.findMatches) == 0 {
		return
	}
	if jumpTop {
		m.viewport.GotoTop()
	}
	m.viewport.SetHighlights(m.findMatches)
}

func (m *Model) moveFindMatch(delta int) {
	if len(m.findMatches) == 0 {
		if m.findQuery != "" {
			m.status = fmt.Sprintf("no matches for '%s'", m.findQuery)
		}
		return
	}
	if delta < 0 {
		m.viewport.HighlightPrevious()
		m.findMatchIndex = (m.findMatchIndex - 1 + len(m.findMatches)) % len(m.findMatches)
	} else {
		m.viewport.HighlightNext()
		m.findMatchIndex = (m.findMatchIndex + 1) % len(m.findMatches)
	}
	m.status = fmt.Sprintf("match %d/%d for '%s'", m.findMatchIndex+1, len(m.findMatches), m.findQuery)
}

func findTranscriptMatches(content, query string) ([][]int, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	re, err := regexp.Compile("(?i)" + query)
	if err != nil {
		re, err = regexp.Compile("(?i)" + regexp.QuoteMeta(query))
		if err != nil {
			return nil, err
		}
	}
	return re.FindAllStringIndex(content, -1), nil
}
