---
name: ai-tracker
description: >-
  Skill for interacting with the AI Tracker CLI tool to manage and monitor local agent tokens and usage. Make sure to use this skill whenever the user asks for telemetry, usage data, API costs, session metrics, or token counts for any agent (Claude Code, Antigravity, Codex), even if they don't explicitly mention 'ait' or 'tracker'.
---

# AI Tracker (ait) Skill

This 2.0 Enterprise skill allows agents to invoke the `ait` CLI tool to query usage metrics for Claude Code, Antigravity, and Codex.

## Usage

Agents can run `ait usage` to get a formatted text table of current token usages and costs directly in the terminal without opening the browser dashboard.

If the agent needs to force a fresh parse of logs, run `ait sync`.

## Data Export for Agents

If you need to retrieve raw usage data (tokens, API costs, sessions) to process programmatically (e.g., to parse in your context or pipe to `jq`), use the export command:

```bash
ait export --json --days <int> --agent <name>
```

- `--days <int>`: (Optional) Limit data to the last `<int>` days.
- `--agent <name>`: (Optional) Filter data by agent name (e.g., `claude`, `antigravity`, `codex`).
- `--json`: Outputs the data in JSON format for easy parsing.
