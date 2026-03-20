# static-html

A local HTML preview server with a CLI for registering HTML files as browser-viewable sessions.

## Requirements

- Node.js 22+

## Install

```bash
npm install
```

## Usage

Start the local server:

```bash
npx html-server start
```

Register an HTML file:

```bash
npx html-server send ./fixtures/basic/index.html
```

The `send` command prints a session URL. Open that URL in a browser to view the HTML file with relative assets served from the source directory.

## Commands

```bash
html-server start [--host 127.0.0.1] [--port 3939]
html-server send <file.html> [--server http://127.0.0.1:3939]
```

## Codex Skill

This repo also ships an installable Codex skill at `skills/static-html-preview`.

Install it from GitHub with the local installer helper:

```bash
~/.codex/skills/.system/skill-installer/scripts/install-skill-from-github.py \
  --repo sun-praise/static-html \
  --path skills/static-html-preview
```

After installing the skill, restart Codex to pick it up.
