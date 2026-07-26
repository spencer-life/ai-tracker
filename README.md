# ai-tracker

`ai-tracker` is a local Go CLI, terminal UI, and browser dashboard for source-backed usage analytics across Codex, Claude Code, and Antigravity (`agy`). It tracks sessions, token categories, models, measurement quality, and API-equivalent cost without inventing missing telemetry.

## Install

Release binaries support Linux/WSL and macOS on x86-64 and ARM64. Windows users should run the Linux release inside WSL.

```bash
mise use -g github:spencer-life/ai-tracker@v1.1.0
ai-tracker version
ai-tracker sync --rebuild
```

The one-time rebuild is required when upgrading from v1.0.0 so checkpoints and sessions are recreated from the effective Codex and Claude configuration homes. It creates a timestamped database backup before changing stored facts.

To follow future stable releases:

```bash
mise use -g github:spencer-life/ai-tracker@latest
mise upgrade github:spencer-life/ai-tracker
```

To remove the managed command later:

```bash
mise use -g --remove github:spencer-life/ai-tracker
mise uninstall github:spencer-life/ai-tracker
```

Alternatively, download the matching archive and `checksums.txt` from [GitHub Releases](https://github.com/spencer-life/ai-tracker/releases), verify the archive before extraction with `sha256sum -c checksums.txt --ignore-missing` on Linux or `shasum -a 256 <archive>` on macOS, and place `ai-tracker` in a directory on `PATH` such as `~/.local/bin`.

Each archive also contains the AI Tracker Codex skill. To install it manually, copy the bundled `skills/ai-tracker` directory to `$CODEX_HOME/skills/ai-tracker` and restart Codex so it discovers the new skill.

## Data sources and trust model

`ai-tracker sync` reads canonical local records:

- **Codex:** `$CODEX_HOME/sessions/**/rollout-*.jsonl` plus a distinct native `~/.codex/sessions/**/rollout-*.jsonl` archive when both exist. Duplicate rollout filenames prefer the active configured store. This includes Windows Codex Desktop and native WSL CLI history without counting same-named copied rollouts twice. Token counts come from new `token_count.last_token_usage` snapshots, including available cache and reasoning fields; repeated cumulative snapshots are deduplicated. Session and parent-thread metadata come from the same rollout.
- **Claude Code:** `$CLAUDE_CONFIG_DIR/projects/**/*.jsonl`, falling back to `~/.claude/projects/**/*.jsonl`. Only assistant `message.usage` records are counted, including cache-read and cache-creation tokens. Session, project, sidechain, and parent metadata come from the project logs.
- **agy / Antigravity:** `~/.gemini/antigravity-cli/conversation_summaries.db` and `conversations/*.db` provide session and subtrajectory metadata. These stores do not expose authoritative token accounting, so agy sessions have no tokens by default. `--include-estimates` can derive opt-in transcript estimates from `brain/**/transcript.jsonl` using character counts; those rows remain labelled `estimated`.

Every event is labelled `reported`, `derived`, `estimated`, or `legacy`. Estimated usage is excluded from normal reports unless `--include-estimates` is explicitly supplied. Missing models and token categories remain unknown or zero as appropriate; they are not filled with fabricated splits.

Costs use a versioned, offline pricing snapshot and are labelled **standard API-equivalent estimates**. The four token buckets are priced separately: uncached input, cache read, cache write, and output; reasoning is not billed again when it is already included in output. Claude's reported 5-minute and 1-hour cache-write breakdown is preserved; when an older row supplies only an aggregate cache-write count, the 5-minute rate is used. The snapshot covers current observed Codex/OpenAI models, Claude Opus 5, Fable 5, Sonnet 5, current Claude 4.x models, Gemini 3.6 Flash (including agy's `-high` alias), Gemini 3.1 Pro, and selected older models. It applies verified long-context tiers and resolves `codex-auto-review` by event date. Pricing is sourced from current vendor documentation and the same pinned ccusage/LiteLLM-style fallbacks used by ccusage v20.0.18.

Unknown model prices remain null. Aggregate views show the priced subtotal plus token/event coverage and exclude unknown-price events instead of treating them as free. These values do not represent subscription billing, priority/fast-tier billing, storage/tool charges, or the amount paid for Codex, Claude, or Gemini plans. Run `ai-tracker sync --rebuild` after a pricing update to reprice existing events from their source records.

The bundled snapshot was checked on 2026-07-25 against [OpenAI model pricing](https://developers.openai.com/api/docs/models), [Anthropic pricing](https://platform.claude.com/docs/en/about-claude/pricing), [Gemini API pricing](https://ai.google.dev/gemini-api/docs/pricing), and [ccusage v20.0.18's pricing implementation](https://github.com/ccusage/ccusage/blob/v20.0.18/rust/crates/ccusage/src/pricing.rs).

## Commands

Start by importing local sources:

```bash
ai-tracker sync
ai-tracker sync --rebuild              # back up, clear all telemetry/checkpoints, and reimport
ai-tracker sync --watch                # poll every five seconds
ai-tracker sync --include-estimates    # opt in to agy transcript estimates
```

Query the same repository used by the TUI and web dashboard. The human-readable report output is a range summary; add `--json` to `daily`, `weekly`, or `monthly` to receive the corresponding bucketed series.

```bash
ai-tracker usage --range 30d
ai-tracker daily --range 7d --tz America/Phoenix --json
ai-tracker weekly --range mtd --json
ai-tracker monthly --range custom --from 2026-01-01 --to 2026-07-01 --json
ai-tracker sessions --range 30d --agent codex
```

`usage`, `daily`, `weekly`, `monthly`, and `sessions` accept `--range today|7d|30d|mtd|custom`, `--from`, `--to`, `--tz`, `--agent`, `--provider`, `--model`, `--quality`, `--include-estimates`, `--limit`, and `--json`. Ranges are half-open (`from` inclusive, `to` exclusive), and weekly buckets start on Monday.

Inspect data health and local agent customizations:

```bash
ai-tracker doctor
ai-tracker doctor --json
ai-tracker inventory
ai-tracker inventory --json
```

`inventory` scans global configuration and the current repository ancestry for supported Codex, Claude, and agy skills, hooks, agents, plugins, rules, and instruction files. It reports metadata and precedence clues; it does not execute discovered components.

Export filtered sessions as JSON, CSV, or a ZIP archive:

```bash
ai-tracker export --range 7d
ai-tracker export --range 30d --csv --out usage.csv
ai-tracker export --range custom --from 2026-07-01 --to 2026-08-01 --out july.json
ai-tracker export --agent claude --csv --out claude.zip
```

Files written with `--out` use mode `0600`. A `.zip` suffix creates a compressed export.

Launch either interface:

```bash
ai-tracker tui
ai-tracker dashboard
ai-tracker dashboard --open
ai-tracker dashboard --port 9090 --no-sync
```

The dashboard defaults to `http://127.0.0.1:8080`, embeds its CSS and JavaScript, and refuses non-loopback listeners because it has no remote authentication. Its REST endpoints are under `/api/v2`; committed browser-triggered sync updates are delivered with server-sent events at `/api/v2/events`.

`ai-tracker clean --yes` creates a backup and clears telemetry plus checkpoints together. Prefer `ai-tracker sync --rebuild` for a recoverable refresh followed immediately by reimport.

## Storage and privacy

The database lives at `~/.config/ai-tracker/data.db` by default; set `AIT_DATA_DIR` to choose another directory. On Linux/WSL and macOS, data directories and backup directories use mode `0700`; databases, backups, and exports use `0600`.

When an old `token_logs` database is first opened, AI Tracker creates a timestamped v1 backup under `~/.config/ai-tracker/backups/` before installing the v2 schema. V1 tables are retained for recovery but are not queried by v2 reports. `sync --rebuild` and `clean --yes` create timestamped backups before clearing all telemetry and checkpoints; legacy rows survive in that backup, not in the rebuilt database.

AI Tracker stores token accounting, timestamps, source-backed session relationships, models, measurement quality, and hashed source/project identifiers. It does not store prompt bodies, transcript text, hook commands, environment values, or full source paths. Opt-in agy estimation counts transcript characters in memory and discards the text. Inventory exposes a basename and stable hash rather than a full path.

## Development

The project uses mise tasks:

```bash
mise run build
mise run test
mise run lint
```

GitHub Actions builds the CLI, runs the complete tests, race detector, `go vet`, and `golangci-lint`, and applies the managed Secretlint scan with read-only repository permissions.

Maintainers should follow the complete [release checklist](RELEASING.md); tag releases are published only after the verification job succeeds.

## License

[MIT](LICENSE) © Innovative Business Solutions
