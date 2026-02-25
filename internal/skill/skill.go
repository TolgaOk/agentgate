package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrg/frontmatter"
)

// Skill represents a parsed SKILL.md file.
// If Tool is non-nil, the skill is a tool skill (has a CLI command).
// Otherwise it's a prompt-only skill.
type Skill struct {
	Name        string
	Description string
	Body        string // markdown body after frontmatter
	FilePath    string // source file path
	Tool        *ToolMeta
}

// ToolMeta defines the CLI command and typed arguments for a tool skill.
type ToolMeta struct {
	Command string
	Args    map[string]ArgDef
}

// ArgDef describes a single argument for a tool skill.
type ArgDef struct {
	Type     string `yaml:"type"`     // "string", "boolean"
	Required bool   `yaml:"required"` // whether the arg is required
	Position int    `yaml:"position"` // 0 = not positional, 1+ = positional ordering
	Flag     string `yaml:"flag"`     // e.g. "--type", "-c", "-a"
	Desc     string `yaml:"desc"`     // description for JSON schema
}

// frontmatterData is the YAML structure at the top of a SKILL.md file.
type frontmatterData struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Metadata    *struct {
		Command string            `yaml:"command"`
		Args    map[string]ArgDef `yaml:"args"`
	} `yaml:"metadata"`
}

// ParseFile parses a single SKILL.md file.
func ParseFile(path string) (Skill, error) {
	f, err := os.Open(path)
	if err != nil {
		return Skill{}, fmt.Errorf("skill: %w", err)
	}
	defer f.Close()

	var fm frontmatterData
	body, err := frontmatter.Parse(f, &fm)
	if err != nil {
		// No frontmatter — treat entire content as body (prompt-only skill).
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return Skill{}, fmt.Errorf("skill: %w", readErr)
		}
		return Skill{
			Name:     nameFromPath(path),
			Body:     strings.TrimSpace(string(data)),
			FilePath: path,
		}, nil
	}

	return buildSkill(fm, strings.TrimSpace(string(body)), path), nil
}

// Parse parses skill content from a string.
func Parse(content, source string) (Skill, error) {
	var fm frontmatterData
	body, err := frontmatter.Parse(strings.NewReader(content), &fm)
	if err != nil {
		// No frontmatter — prompt-only skill.
		return Skill{
			Name:     nameFromPath(source),
			Body:     strings.TrimSpace(content),
			FilePath: source,
		}, nil
	}

	return buildSkill(fm, strings.TrimSpace(string(body)), source), nil
}

func buildSkill(fm frontmatterData, body, source string) Skill {
	s := Skill{
		Name:        fm.Name,
		Description: fm.Description,
		Body:        body,
		FilePath:    source,
	}

	if fm.Metadata != nil && fm.Metadata.Command != "" {
		s.Tool = &ToolMeta{
			Command: fm.Metadata.Command,
			Args:    fm.Metadata.Args,
		}
	}

	if s.Name == "" {
		s.Name = nameFromPath(source)
	}

	return s
}

// ParseDir loads all .md files from a directory (non-recursive).
func ParseDir(dir string) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("skill: read dir %s: %w", dir, err)
	}

	var skills []Skill
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		s, err := ParseFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		skills = append(skills, s)
	}
	return skills, nil
}

// IsTool returns true if this skill has a CLI command (tool skill).
func (s *Skill) IsTool() bool {
	return s.Tool != nil
}

// nameFromPath extracts a skill name from a file path.
// "SKILL.md" or "skill.md" → parent directory name, otherwise filename without extension.
func nameFromPath(path string) string {
	base := filepath.Base(path)
	lower := strings.ToLower(base)

	if lower == "skill.md" {
		return filepath.Base(filepath.Dir(path))
	}

	return strings.TrimSuffix(base, filepath.Ext(base))
}
