# AgentGate Hub

Go-based LLM agent hub. Runs an agentic loop with tool execution (bash), rate limiting, policy enforcement, and session persistence.

> Tools are extended via skills located as markdown files at (`~/.config/agentgate/skills/`) that teach the agent how to use specific CLIs.

**Sessions**

Sessions persist the full conversation (messages, tool calls, results, metadata) as JSONL files. On resume, the file is loaded and sent as conversation history — the LLM picks up where it left off.

## Install

```
go install github.com/TolgaOk/agentgate/cmd/ag@latest
```

## Usage

```bash
# Singleton prompt
ag ask "What files are in this directory?"

# Interactive sessions
ag session
ag session --resume <id>
ag session list
```

Sessions are stored in `~/.local/share/agentgate/sessions/` by default (override with `--dir`).

## Configuration

```
~/.config/agentgate/
  config.toml       # provider, model, limits
  policy.toml       # command blocking rules
  system.md         # system prompt
  skills/           # skill files (markdown)
```

`config.toml`:

```toml
provider = "anthropic"          # "anthropic" or "openrouter"
model = "claude-sonnet-4-6"     # model ID (provider-specific)
max_tokens = 8192
rate_limit_rpm = 50
```

**Environment Variables**

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_API_KEY` | Required for Anthropic provider |
| `OPENROUTER_API_KEY` | Required for OpenRouter provider |
| `AG_PROVIDER` | Override provider (overrides config) |
| `AG_MODEL` | Override model (overrides config) |
