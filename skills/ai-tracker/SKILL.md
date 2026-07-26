---
name: ai-tracker
description: >-
  Sync, inspect, export, and diagnose local Codex, Claude Code, and agy usage
  with the ai-tracker CLI. Use for session or token reports, daily, weekly, or monthly
  trends, API-equivalent costs, data-health checks, dashboard or TUI launches,
  and privacy-safe inventories of skills, hooks, agents, plugins, and rules.
---

# AI Tracker (`ai-tracker`)

## Safe workflow

1. Run `command -v ai-tracker` and `ai-tracker version`. If it is missing, report that unless the user asked to install or update it; for an authorized install use `mise use -g github:spencer-life/ai-tracker@latest`.
2. Run `ai-tracker sync` when current local data is required. Use `ai-tracker sync --rebuild` only for an authorized recoverable refresh; it creates a database backup first. Run one rebuild after upgrading from v1.0.0 to v1.1.0.
3. Query with `ai-tracker usage`, `ai-tracker daily`, `ai-tracker weekly`, `ai-tracker monthly`, or `ai-tracker sessions`.
4. Prefer `--json` when another tool or agent will consume the result.
5. State measurement quality and whether estimates were included. Never describe API-equivalent cost as an invoice or subscription charge.

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

- Codex tokens are reported by `$CODEX_HOME/sessions/**/rollout-*.jsonl` plus a distinct native `~/.codex/sessions` archive. Duplicate rollout filenames prefer the active configured store, covering Codex Desktop and native WSL CLI history without double-counting same-named copied files.
- Claude tokens are reported by assistant `message.usage` records under `$CLAUDE_CONFIG_DIR/projects`, falling back to `~/.claude/projects`.
- agy provides session metadata from its local summary/conversation databases but no authoritative tokens. `ai-tracker sync --include-estimates` imports character-derived transcript estimates, and report commands also require `--include-estimates` to show them.
- Cost is a versioned standard API-equivalent estimate, not subscription or priority/fast-tier billing. Current Codex/OpenAI, Claude Opus 5/Fable 5/Sonnet 5, Gemini 3.6 Flash, and Gemini 3.1 Pro rates are embedded. Claude 5-minute and 1-hour cache writes are distinct when reported; aggregate-only legacy cache writes use the 5-minute rate. Unknown models remain null; aggregate cost excludes them and reports pricing coverage. Rebuild after pricing/parser upgrades to reprice historical events.

`ai-tracker inventory` reads bounded structural metadata for global configuration and the current repository ancestry. It does not execute customizations or expose prompt/rule bodies, hook commands, environment values, or full paths.

Use `ai-tracker tui` for the terminal dashboard or `ai-tracker dashboard` for the embedded browser dashboard. The browser server is loopback-only and exposes `/api/v2` locally.

Official release binaries cover Linux/WSL and macOS on x86-64 and ARM64. On Windows, use the Linux release inside WSL.
