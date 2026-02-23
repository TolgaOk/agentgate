# AgentGAte (`aga`)

[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![v0.1.0-alpha](https://img.shields.io/badge/v0.1.0--alpha-orange)]()

LLM agent hub with tool execution, rate limiting, and context persistence. Designed for agentic usage (by agents) and to serve as the backend for agentic workflows.

> **Philosophy**: Extend the tools by adding `Skill.md` with a `CLI`.

## Install

```
go install github.com/TolgaOk/agentgate/cmd/aga@latest
```

## Usage

```bash
aga ask "What files are in this directory?"           # create conversation
aga ask --json "summarize this project"               # JSON output (for agentic call)
aga ask --context /tmp/session.json  "now fix it"     # continue a conversation
aga ask --skill ./skills "say hi in whatsapp"         # with custom skill directory

aga auth openai                                       # sign in with ChatGPT (subscription)
aga auth status                                       # check token status
```

## How it works

**Agent Loop**
```
  ┌──────────┐   ┌────────┐   ┌──────────┐   ┌───────────┐
 ┌──────────┐│   │        │──▶│ LLM API  │──▶│ text,     │
┌──────────┐││   │        │   │(+context)│   │ context   │
│ aga ask  ││───▶│        │   └────┬─────┘   └───────────┘
│ "prompt" │┘    │ SQLite │  tool? │
└──────────┘     │        │        ▼
                 │        │   ┌──────────┐
                 │        │◀──┤  tool    │
                 │        │   │ execute  │
                 │        │   └──────────┘
                 └────────┘
                     ▲ limit concurrent requests, usage tracking
```

## Providers

- `openai`
  - Subscription: `aga auth openai` (primary)
  - API key: `OPENAI_API_KEY` (fallback)
- `anthropic` (subscription is not available)
  - API key: `ANTHROPIC_API_KEY`
- `openrouter`
  - API key: `OPENROUTER_API_KEY`

## Configuration

```
~/.config/agentgate/
  config.toml       
  system.md         # system prompt
  tokens.json       # OAuth tokens (auto-managed)
  skills/           # skill files (markdown)
```

`config.toml`

```toml
provider = "openai"
model = "gpt-5.2"
max_tokens = 8192
max_steps = 20
concurrent_global_limit = 3
concurrent_per_provider_limit = 1
```
