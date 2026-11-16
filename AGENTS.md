# AGENTS.md

This checkout is the public Cursor community repo plus the Grok Bot access preview.

## Cursor Cloud specific instructions

- From the repo root, `npm start` serves Grok Bot access at http://127.0.0.1:4173. Local requests use the simulated viewer in `grok-bot-access/cursor/identity.dev.js`.
- `.cursor/environment.json` installs that package and starts the same server.
- `npm run docs:health` audits README, SECURITY, and the issue template. It needs `CURSOR_API_KEY` or a prior `npm run sdk:login`.
