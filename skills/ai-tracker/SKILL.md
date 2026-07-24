---
name: ai-tracker
description: >-
  Use the local AI Tracker CLI to sync and query source-backed Codex, Claude
  Code, and agy sessions, token usage, API-equivalent costs, data health, and
  privacy-safe customization inventory.
---

# AI Tracker (`ai-tracker`)

Use this skill when a request concerns local Codex, Claude Code, or agy usage, sessions, token counts, cost estimates, data health, or configured skills/hooks/agents/plugins.

## Safe workflow

1. Run `ai-tracker sync` when current local data is required. Use `ai-tracker sync --rebuild` only when the user asks for a recoverable full refresh; it creates a database backup first.
2. Query with `ai-tracker usage`, `ai-tracker daily`, `ai-tracker weekly`, `ai-tracker monthly`, or `ai-tracker sessions`.
3. Prefer `--json` when another tool or agent will consume the result.
4. State measurement quality and whether estimates were included. Never describe API-equivalent cost as an invoice or subscription charge.

Examples:

```bash
ai-tracker usage --range 30d --json
ai-tracker daily --range 7d --tz America/Phoenix --json
ai-tracker weekly --range mtd --agent claude --json
ai-tracker monthly --range custom --from 2026-01-01 --to 2026-07-01 --json
ai-tracker sessions --range 30d --provider openai --limit 100 --json
ai-tracker doctor --json
ai-tracker inventory --json
ai-tracker export --range 30d --agent codex
```

Common report and export filters are `--range today|7d|30d|mtd|custom`, `--from`, `--to`, `--tz`, `--agent`, `--provider`, `--model`, `--quality`, `--include-estimates`, and `--limit`. `--from` is inclusive and `--to` is exclusive.

## Source and quality semantics

- Codex tokens are reported by `~/.codex/sessions/**/rollout-*.jsonl`.
- Claude tokens are reported by assistant `message.usage` records in `~/.claude/projects/**/*.jsonl`.
- agy provides session metadata from its local summary/conversation databases but no authoritative tokens. `ai-tracker sync --include-estimates` imports character-derived transcript estimates, and report commands also require `--include-estimates` to show them.
- Unknown model prices remain null. Cost is a versioned API-equivalent estimate only.

`ai-tracker inventory` reads bounded structural metadata for global configuration and the current repository ancestry. It does not execute customizations or expose prompt/rule bodies, hook commands, environment values, or full paths.

Use `ai-tracker tui` for the terminal dashboard or `ai-tracker dashboard` for the embedded browser dashboard. The browser server is loopback-only and exposes `/api/v2` locally.
