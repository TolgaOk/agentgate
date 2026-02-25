package skill

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/TolgaOk/agentgate/internal/provider"
)

// ToToolDef converts a tool skill into a provider.ToolDef with a JSON Schema InputSchema.
// Panics if the skill is not a tool skill (Tool == nil).
func (s *Skill) ToToolDef() provider.ToolDef {
	if s.Tool == nil {
		panic("skill: ToToolDef called on prompt-only skill " + s.Name)
	}

	properties := map[string]map[string]string{}
	var required []string

	for name, arg := range s.Tool.Args {
		prop := map[string]string{
			"type": arg.Type,
		}
		if arg.Type == "" {
			prop["type"] = "string"
		}
		if arg.Desc != "" {
			prop["description"] = arg.Desc
		}
		properties[name] = prop

		if arg.Required {
			required = append(required, name)
		}
	}
	sort.Strings(required)

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}

	raw, _ := json.Marshal(schema)

	return provider.ToolDef{
		Name:        s.Name,
		Description: s.Description,
		InputSchema: raw,
	}
}

// BuildArgv maps LLM JSON parameters to a CLI argv slice.
// The first element is always the command binary.
//
// Mapping rules:
//   - position: N (1+) → positional arg at that index
//   - flag + boolean true → append flag (e.g. "-a")
//   - flag + string value → append flag then value (e.g. "--type" "go")
//   - boolean false / empty → omitted
func (s *Skill) BuildArgv(rawJSON string) ([]string, error) {
	if s.Tool == nil {
		return nil, fmt.Errorf("skill: BuildArgv called on prompt-only skill %s", s.Name)
	}

	var params map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawJSON), &params); err != nil {
		return nil, fmt.Errorf("skill: parse input JSON: %w", err)
	}

	// Check required args.
	for name, arg := range s.Tool.Args {
		if arg.Required {
			if _, ok := params[name]; !ok {
				return nil, fmt.Errorf("skill: missing required argument %q", name)
			}
		}
	}

	argv := []string{s.Tool.Command}

	// Collect positional args (sorted by position).
	type positional struct {
		pos   int
		value string
	}
	var positionals []positional

	// Collect flag args.
	var flags []string

	for name, arg := range s.Tool.Args {
		raw, ok := params[name]
		if !ok {
			continue
		}

		value, err := resolveValue(arg.Type, raw)
		if err != nil {
			return nil, fmt.Errorf("skill: argument %q: %w", name, err)
		}

		if arg.Position > 0 {
			positionals = append(positionals, positional{pos: arg.Position, value: value})
			continue
		}

		if arg.Flag != "" {
			if arg.Type == "boolean" {
				if value == "true" {
					flags = append(flags, arg.Flag)
				}
			} else {
				flags = append(flags, arg.Flag, value)
			}
		}
	}

	sort.Strings(flags)
	argv = append(argv, flags...)

	sort.Slice(positionals, func(i, j int) bool {
		return positionals[i].pos < positionals[j].pos
	})
	for _, p := range positionals {
		argv = append(argv, p.value)
	}

	return argv, nil
}

// resolveValue extracts the string representation from a JSON value.
func resolveValue(typ string, raw json.RawMessage) (string, error) {
	switch typ {
	case "boolean":
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return "", fmt.Errorf("expected boolean: %w", err)
		}
		return strconv.FormatBool(b), nil
	default: // "string" or unspecified
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", fmt.Errorf("expected string: %w", err)
		}
		return s, nil
	}
}
