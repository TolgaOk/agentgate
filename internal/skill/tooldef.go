package skill

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/TolgaOk/agentgate/internal/provider"
)

// ToToolDef converts a tool skill into a provider.ToolDef with a JSON Schema InputSchema.
// If the skill has subcommands, a "subcommand" enum property is added and all
// subcommand args are merged into the schema.
// Panics if the skill is not a tool skill (Tool == nil).
func (s *Skill) ToToolDef() provider.ToolDef {
	if s.Tool == nil {
		panic("skill: ToToolDef called on prompt-only skill " + s.Name)
	}

	properties := map[string]any{}
	var required []string

	// If subcommands exist, add enum property and merge their args.
	if len(s.Tool.Subcommands) > 0 {
		var names []string
		var descs []string
		for name, sub := range s.Tool.Subcommands {
			names = append(names, name)
			if sub.Desc != "" {
				descs = append(descs, name+": "+sub.Desc)
			}
		}
		sort.Strings(names)
		sort.Strings(descs)

		subcmdProp := map[string]any{
			"type": "string",
			"enum": names,
		}
		if len(descs) > 0 {
			subcmdProp["description"] = strings.Join(descs, "; ")
		}
		properties["subcommand"] = subcmdProp
		required = append(required, "subcommand")

		// Merge args from all subcommands.
		for _, sub := range s.Tool.Subcommands {
			for name, arg := range sub.Args {
				if _, exists := properties[name]; exists {
					continue // already added by another subcommand
				}
				prop := map[string]string{"type": arg.Type}
				if arg.Type == "" {
					prop["type"] = "string"
				}
				if arg.Desc != "" {
					prop["description"] = arg.Desc
				}
				properties[name] = prop
			}
		}
	} else {
		// No subcommands — flat args.
		for name, arg := range s.Tool.Args {
			prop := map[string]string{"type": arg.Type}
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
//   - subcommand param → inserted after command (e.g. "aga" "ask" ...)
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

	argv := []string{s.Tool.Command}

	// Resolve which args to use.
	args := s.Tool.Args
	if len(s.Tool.Subcommands) > 0 {
		// Extract subcommand from params.
		raw, ok := params["subcommand"]
		if !ok {
			return nil, fmt.Errorf("skill: missing required argument %q", "subcommand")
		}
		var subcmd string
		if err := json.Unmarshal(raw, &subcmd); err != nil {
			return nil, fmt.Errorf("skill: argument %q: expected string: %w", "subcommand", err)
		}
		sub, ok := s.Tool.Subcommands[subcmd]
		if !ok {
			return nil, fmt.Errorf("skill: unknown subcommand %q", subcmd)
		}
		argv = append(argv, subcmd)
		args = sub.Args
		delete(params, "subcommand")
	}

	// Check required args.
	for name, arg := range args {
		if arg.Required {
			if _, ok := params[name]; !ok {
				return nil, fmt.Errorf("skill: missing required argument %q", name)
			}
		}
	}

	// Collect positional args (sorted by position).
	type positional struct {
		pos   int
		value string
	}
	var positionals []positional

	// Collect flag args as pairs (flag + optional value).
	type flagPair struct {
		flag  string
		value string // empty for boolean flags
	}
	var flags []flagPair

	for name, arg := range args {
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
					flags = append(flags, flagPair{flag: arg.Flag})
				}
			} else if value != "" {
				flags = append(flags, flagPair{flag: arg.Flag, value: value})
			}
		}
	}

	sort.Slice(flags, func(i, j int) bool { return flags[i].flag < flags[j].flag })
	for _, f := range flags {
		argv = append(argv, f.flag)
		if f.value != "" {
			argv = append(argv, f.value)
		}
	}

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
