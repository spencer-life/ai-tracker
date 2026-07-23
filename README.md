<div align="center">
  <img src="assets/banner.jpg" alt="AI Tracker Banner" width="100%">
  
  <h1>ai-tracker</h1>
  <p><strong>Passive Log-Tailing Analytics Dashboard for Local AI Agents</strong></p>
  
  [![Go Report Card](https://goreportcard.com/badge/github.com/spencer-life/ai-tracker)](https://goreportcard.com/report/github.com/spencer-life/ai-tracker)
  [![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
  
  Tags: `golang`, `cli`, `ai`, `dashboard`, `tui`, `catppuccin`, `bubbletea`, `tailwindcss`
</div>

---

## 🚀 Overview

`ai-tracker` (`ait`) is a hyper-fast, zero-friction CLI and local web dashboard for monitoring your local AI agent usage across multiple platforms:
- **Claude Code** (`~/.claude/logs`)
- **Antigravity** (`~/.gemini/antigravity-cli/brain/`)
- **Codex** (`~/.codex/`)

It passively tails their telemetry JSON logs using Go concurrency, extracting tokens and calculating "Shadow API Costs" instantly, wrapping it in a premium **Catppuccin Frappe** UI.

## ✨ Features

- **Embedded Web Dashboard:** A single Go binary that serves a beautiful Tailwind + GSAP + Lenis dashboard. Dark/Light mode supported natively.
- **Terminal UI (TUI):** A stunning Bubbletea-powered terminal dashboard for quick checks without leaving your flow.
- **Zero-Friction Ingestion:** No wrappers required. Just run your agents naturally, and `ait` parses the disk logs.
- **Cost Calculation:** Translates subscription token usage into equivalent API spend (e.g. `gemini-1.5-pro` and `claude-3.5-sonnet`).

## 🛠️ Usage

```bash
# Start the local web dashboard on http://localhost:8080
ait dashboard

# Launch the interactive Terminal UI (TUI)
ait tui

# Force a manual sync of all agent logs
ait sync
```
