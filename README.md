# Cursor

For **bugs and feature requests**, use the [community forum](https://forum.cursor.com/) rather than GitHub Issues—we monitor the forum as our primary channel.

## Getting Started

Head over to [our website](https://cursor.com/) to download Cursor.

## Features

[See here](https://cursor.com/features) for more info on Cursor's features.

## Resources

- Download: https://cursor.com
- Features: https://cursor.com/features
- Community forum: https://forum.cursor.com/
- Bugs & feedback: [forum.cursor.com](https://forum.cursor.com/) (preferred over GitHub Issues)

## Security

Please do not report security vulnerabilities through public issues. See [SECURITY.md](SECURITY.md) and email [security-reports@cursor.com](mailto:security-reports@cursor.com).

## Docs healthcheck (Cursor SDK)

Maintainers can run a local Cursor agent against this checkout to audit README, SECURITY, and issue templates before merging doc changes:

```bash
npm install
# either:
export CURSOR_API_KEY=cursor_...   # https://cursor.com/dashboard/cloud-agents
# or one-time browser login:
npm run sdk:login

npm run docs:health
```

Uses `@cursor/sdk` `Agent.prompt` against the local tree (including uncommitted edits) and writes `docs-health-report.md`. On pull requests that touch these docs, `.github/workflows/docs-healthcheck.yml` runs the same audit when the `CURSOR_API_KEY` repository secret is set.
