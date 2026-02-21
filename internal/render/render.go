package render

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/glamour"
)

// StreamRenderer progressively renders markdown as text is appended.
// It re-renders the full accumulated text with glamour, overwriting
// the previous output using ANSI escape codes.
type StreamRenderer struct {
	out       io.Writer
	renderer  *glamour.TermRenderer
	mu        sync.Mutex
	buf       strings.Builder
	lineCount int
	dirty     bool
	done      chan struct{}
}

// Style selects the glamour color scheme.
type Style string

const (
	StyleAuto  Style = "auto"
	StyleDark  Style = "dark"
	StyleLight Style = "light"
)

func NewStreamRenderer(out io.Writer, style Style) (*StreamRenderer, error) {
	var opt glamour.TermRendererOption
	switch style {
	case StyleDark:
		opt = glamour.WithStylePath("dark")
	case StyleLight:
		opt = glamour.WithStylePath("light")
	default:
		opt = glamour.WithAutoStyle()
	}

	r, err := glamour.NewTermRenderer(
		opt,
		glamour.WithWordWrap(100),
	)
	if err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}
	sr := &StreamRenderer{
		out:      out,
		renderer: r,
		done:     make(chan struct{}),
	}
	go sr.refreshLoop()
	return sr, nil
}

func (sr *StreamRenderer) Write(p []byte) (int, error) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	n, _ := sr.buf.Write(p)
	sr.dirty = true
	return n, nil
}

func (sr *StreamRenderer) Finish() {
	close(sr.done)
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.render()
}

func (sr *StreamRenderer) Text() string {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	return sr.buf.String()
}

func (sr *StreamRenderer) refreshLoop() {
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			sr.mu.Lock()
			if sr.dirty {
				sr.render()
				sr.dirty = false
			}
			sr.mu.Unlock()
		case <-sr.done:
			return
		}
	}
}

func (sr *StreamRenderer) render() {
	text := sr.buf.String()
	if text == "" {
		return
	}

	rendered, err := sr.renderer.Render(text)
	if err != nil {
		rendered = text
	}

	if sr.lineCount > 0 {
		fmt.Fprintf(sr.out, "\033[%dA\033[J", sr.lineCount)
	}

	fmt.Fprint(sr.out, rendered)
	sr.lineCount = strings.Count(rendered, "\n")
}
