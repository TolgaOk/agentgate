# AgentGate 

[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![v0.1.0-alpha](https://img.shields.io/badge/v0.1.0--alpha-orange)]()

`aga` (short for Agent Gate) is a lightweight LLM hub designed for agentic workflows (humans welcome). It comes with built-in concurrency, tool execution, rate limiting, and conversation persistence.


## Install

```
go install github.com/TolgaOk/agentgate/cmd/aga@latest
```

## Usage

```bash
aga ask "What files are in this directory?"           # create conversation
aga ask --json "summarize this project"               # JSON output (for agentic call)
aga ask --context /tmp/session.json  "now fix it"     # continue a conversation (or start a new one)
aga ask --skill ./skills "say hi in whatsapp"         # with custom skill directory

aga auth openai                                       # sign in with ChatGPT (subscription)
aga auth status                                       # check token status

aga metrics                                           # llm usage summary
```

See `aga ask --help` for more options.

## How it works

**Agent Loop**
```
  ┌──────────┐   ┌────────┐   ┌──────────┐   ┌──────────────┐
 ┌──────────┐│   │        │──▶│ LLM API  │──▶│ text,        │
┌──────────┐││   │        │   │(+context)│   │ context.json │
│ aga ask  ││───▶│        │   └────┬─────┘   └──────────────┘
│ "prompt" │┘    │ SQLite │  tool? │
└──────────┘     │        │        ▼
                 │        │   ┌──────────┐
                 │        │◀──┤ tool exec│
                 │        │   │(+context)│
                 │        │   └──────────┘
                 └────────┘
                     ▲ 
                     └── limit concurrent requests, usage tracking
```

> **Persistency**: Each conversation is saved.

`aga` preserves the chat history in `context.json` for each conversation, updated incrementally with each LLM and tool call.

> **Concurrency**: SQLite + heartbeat. 

Concurrent LLM calls are limited per provider via SQLite. A heartbeat mechanism automatically frees slots from crashed processes.

> **Tools**: Extend the tools by adding `skill.md` that describes a `CLI` tool.

Tools can be registered as special skills that describe the CLI tool within the frontmatter inside the `metadata` attribute. For example:

`skills/ls.md`
```markdown
---
name: ls
description: List directory contents
metadata:
  command: ls
  args:
    path:
      type: string
      position: 1
      desc: directory to list
    all:
      type: boolean
      flag: "-a"
      desc: show hidden files
---
<!-- No body is needed for tool skills -->
```

**Note**: `aga` comes barebones with no tools or skills.

## Providers

- `openai`
  - Use subscription: `aga auth openai` (primary)
  - Requires API key: `OPENAI_API_KEY` (fallback)
- `anthropic` (subscription is not supported)
  - Requires API key: `ANTHROPIC_API_KEY`
- `openrouter`
  - Requires API key: `OPENROUTER_API_KEY`

## Configuration

```
~/.config/agentgate/
  config.toml       
  system.md         # system prompt
  tokens.json       # OAuth tokens (auto-managed)
  skills/           # skill folder
```

`config.toml`

```toml
provider = "openai"                # openai, anthropic, openrouter
model = "gpt-5.2"                  # model name for the provider
max_tokens = 8192                  # max output tokens per LLM call
max_steps = 20                     # max agent loop iterations
concurrent_global_limit = 3        # max concurrent LLM calls across all providers
concurrent_per_provider_limit = 1  # max concurrent LLM calls per provider

[policy]                           # tool execution policy 
timeout = "30s"                    # default timeout for tool execution
allowed = ["ls"]                   # auto accept
blocked = ["sudo"]                 # always reject
```

> **Policy**: Tools not in `allowed` require y/N confirmation. Tools in `blocked` are always rejected.

Use `--auto-accept` to skip confirmations for agentic use (blocked tools are still rejected).
