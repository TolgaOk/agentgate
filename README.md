# ag

[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![v0.1.0-alpha](https://img.shields.io/badge/v0.1.0--alpha-orange)]()

LLM agent hub with tool execution, rate limiting, and context persistence. Designed for agentic (by agents) usage and to serve as the backend for agentic workflows.

> **Philosophy**: A tool is a `CLI` + `Skill.md`.

## Install

```
go install github.com/TolgaOk/agentgate/cmd/ag@latest
```

## Usage

```bash
ag ask "What files are in this directory?"           # create conversation
ag ask --jeon "summarize this project"               # JSON output (for agentic call)
ag ask --context /tmp/session.jsonl "now fix it"     # continue a conversation
ag ask --skill ./skills "say hi in whatsapp"         # with custom skill directory

ag auth openai                                       # sign in with ChatGPT (subscription)
ag auth status                                       # check token status
```

## How it works

```
┌──────────┐   ┌────────────┐   ┌─────────┐   ┌──────────┐   ┌────────────┐
│ ag ask   │──▶│ Agent Loop │──▶│ LLM API │──▶│  tool    │──▶│ output +   │
│ "prompt" │   │ (≤n steps) │   │ (stream)│   │ execute  │   │ stdout +   │
└────┬─────┘   └─────┬──────┘   └─────────┘   └──────────┘   │ context    │
     │               │                                       └────────────┘
┌────▼─────┐   ┌─────▼──────┐
│ context  │   │  SQLite    │ ◀── concurrency limits, usage tracking
└──────────┘   └────────────┘
```

## Providers

- `openai`
  - Subscription: `ag auth openai` (primary)
  - API key: `OPENAI_API_KEY` (fallback)
- `anthropic` (subscription is not available)
  - API key: `ANTHROPIC_API_KEY`
- `openrouter`
  - API key: `OPENROUTER_API_KEY`

## Configuration

```
~/.config/agentgate/
  config.toml       # provider, model, limits
  system.md         # system prompt
  tokens.json       # OAuth tokens (auto-managed)
  skills/           # skill files (markdown)
```

```toml
provider = "openai"
model = "gpt-5.2"
max_tokens = 8192
max_steps = 20
timeout = "120s"
concurrent_global_limit = 3
concurrent_per_provider_limit = 1
```
