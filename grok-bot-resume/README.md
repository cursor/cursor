# Grok Bot — resume agents (Python)

Resume an existing Cursor agent with `cursor-sdk` and send a follow-up.
`bc-…` ids are cloud; other ids resume local against `--cwd`.

## Setup

```bash
cd grok-bot-resume
python3 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
export CURSOR_API_KEY="cursor_…"   # Dashboard → Integrations
```

## Resume

```bash
python resume_agent.py bc-YOUR_AGENT_ID "Continue from where we left off" --stream
```

Local agent:

```bash
python resume_agent.py local-agent-id "Also update the changelog" --cwd /path/to/repo
```

Re-attach inline MCP on resume (not persisted). Project X MCP lives in
`.cursor/mcp.json`; the resume CLI expects the inner `mcpServers` map shape in
`mcp.servers.json`:

```bash
python resume_agent.py bc-… "Use the tools again" --mcp-json ./mcp.servers.json
```

Exit codes: `0` finished · `1` startup/`CursorAgentError` · `2` run executed but failed.

## Notes

- Pass `CURSOR_API_KEY` explicitly via env; never commit it.
- X MCP OAuth uses `auth.CLIENT_ID` + scopes (no bearer pasted in chat). Complete Connect / OAuth in Cursor; resume must re-pass `--mcp-json`.
- **agent-sdk-mcp-auth** (`@grok/agent-sdk` `defineConnection`) is a different stack than this Cursor SDK CLI.
- Keep this folder **outside** `grok-bot-access/` so publish-app does not ship the CLI.

