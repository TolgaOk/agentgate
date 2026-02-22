# AgentGate Hub

> v0.1.0-alpha

Go-based LLM agent hub. Runs an agentic loop with tool execution (bash), rate limiting, policy enforcement, and conversation context persistence.

> Tools are extended via skill files (markdown) that teach the agent how to use specific CLIs.

## Install

```
go install github.com/TolgaOk/agentgate/cmd/ag@latest
```

## Usage

```bash
# Run a prompt
ag ask "What files are in this directory?"

# JSON output
ag ask --json "summarize this project"

# Continue a conversation
ag ask --context /tmp/agentgate/2026-02-22_00-18-59.jsonl "now fix it"

# Pipe input
echo "explain this" | ag ask
```

Every call saves conversation context to a JSONL file (in `$TMPDIR/agentgate/` by default). Pass `--context` to load and extend an existing conversation.

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
