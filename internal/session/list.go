package session

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// SessionInfo is a summary of a session for listing.
type SessionInfo struct {
	ID      string
	Path    string
	Model   string
	Started time.Time
	Preview string // first user message, truncated
}

// List returns session summaries from JSONL files in dir, sorted newest first.
func List(dir string) ([]SessionInfo, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil, err
	}

	var infos []SessionInfo
	for _, path := range matches {
		info, err := readSessionInfo(path)
		if err != nil {
			continue
		}
		infos = append(infos, info)
	}

	slices.SortFunc(infos, func(a, b SessionInfo) int {
		return b.Started.Compare(a.Started)
	})

	return infos, nil
}

func readSessionInfo(path string) (SessionInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return SessionInfo{}, err
	}
	defer f.Close()

	header, msgs, err := Parse(f)
	if err != nil {
		return SessionInfo{}, err
	}

	id, started := idFromFilename(path)

	preview := ""
	for _, m := range msgs {
		if m.Role == "user" && m.Content != "" {
			preview = truncate(m.Content, 60)
			break
		}
	}

	return SessionInfo{
		ID:      id,
		Path:    path,
		Model:   header.Model,
		Started: started,
		Preview: preview,
	}, nil
}

func idFromFilename(path string) (string, time.Time) {
	base := filepath.Base(path)
	id := strings.TrimSuffix(base, filepath.Ext(base))
	t, _ := time.Parse("2006-01-02_15-04-05", id)
	return id, t
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
