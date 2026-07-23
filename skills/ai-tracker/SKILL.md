---
name: ai-tracker
description: Skill for interacting with the AI Tracker CLI tool to manage and monitor local agent tokens and usage.
---

# AI Tracker (ait) Skill

This skill allows agents to invoke the `ait` CLI tool to query usage metrics for Claude Code, Antigravity, and Codex.

## Usage

Agents can run `ait usage` to get a formatted text table of current token usages and costs directly in the terminal without opening the browser dashboard.

If the agent needs to force a fresh parse of logs, run `ait sync`.
