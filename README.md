# ai-tracker

`ai-tracker` is a local Go CLI, terminal UI, and browser dashboard for source-backed usage analytics across Codex, Claude Code, and Antigravity (`agy`). It tracks sessions, token categories, models, measurement quality, and API-equivalent cost without inventing missing telemetry.

## Data sources and trust model

`ai-tracker sync` reads canonical local records:

- **Codex:** `~/.codex/sessions/**/rollout-*.jsonl`. Token counts come from new `token_count.last_token_usage` snapshots, including available cache and reasoning fields; repeated cumulative snapshots are deduplicated. Session and parent-thread metadata come from the same rollout.
- **Claude Code:** `~/.claude/projects/**/*.jsonl`. Only assistant `message.usage` records are counted, including cache-read and cache-creation tokens. Session, project, sidechain, and parent metadata come from the project logs.
- **agy / Antigravity:** `~/.gemini/antigravity-cli/conversation_summaries.db` and `conversations/*.db` provide session and subtrajectory metadata. These stores do not expose authoritative token accounting, so agy sessions have no tokens by default. `--include-estimates` can derive opt-in transcript estimates from `brain/**/transcript.jsonl` using character counts; those rows remain labelled `estimated`.

Every event is labelled `reported`, `derived`, `estimated`, or `legacy`. Estimated usage is excluded from normal reports unless `--include-estimates` is explicitly supplied. Missing models and token categories remain unknown or zero as appropriate; they are not filled with fabricated splits.

Costs use a versioned pricing snapshot embedded in the binary and are labelled **API-equivalent estimates**. The initial snapshot covers Claude 3.5 Sonnet aliases and Gemini 1.5 Pro; current local Codex and newer Claude model names intentionally remain null until a verified price is added. These values do not represent subscription billing.

## Commands

Start by importing local sources:

```bash
ai-tracker sync
ai-tracker sync --rebuild              # create a backup, clear v2 facts/checkpoints, and reimport
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

The database lives at `~/.config/ai-tracker/data.db` by default; set `AIT_DATA_DIR` to choose another directory. Data directories and backup directories use mode `0700`; databases, backups, and exports use `0600`.

When an old `token_logs` database is first opened, AI Tracker creates a timestamped v1 backup under `~/.config/ai-tracker/backups/` before installing the v2 schema. V1 tables are retained for recovery but are not queried by v2 reports. `sync --rebuild` and `clean --yes` also create timestamped backups before clearing v2 data.

AI Tracker stores token accounting, timestamps, source-backed session relationships, models, measurement quality, and hashed source/project identifiers. It does not store prompt bodies, transcript text, hook commands, environment values, or full source paths. Opt-in agy estimation counts transcript characters in memory and discards the text. Inventory exposes a basename and stable hash rather than a full path.

## Development

The project uses mise tasks:

```bash
mise run build
mise run test
mise run lint
```

GitHub Actions builds the CLI, runs the complete tests, race detector, `go vet`, and `golangci-lint`, and applies the managed Secretlint scan with read-only repository permissions.

## License

[MIT](LICENSE) © Innovative Business Solutions
