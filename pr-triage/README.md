# pr-triage

GitHub-driven Agent SDK agent. Reviews pull requests on `okaybrah/cursor`
and labels PRs when a check suite fails.

## Local loop

```bash
cd pr-triage
npx --yes "@grok/agent-sdk@0.6.0" validate --dir .
npx --yes "@grok/agent-sdk@0.6.0" info --dir . --json
npm run check
npx --yes "@grok/agent-sdk@0.6.0" login
npx --yes "@grok/agent-sdk@0.6.0" serve --dir . --mode single --dev
```

Playground: http://127.0.0.1:3000/playground

Watched repos are listed in `bot/lib/repos.ts`. Pass the same list at serve/deploy.

### Fixture (unsigned loopback)

```bash
# --mode single mounts at /v1/... (multi would use /pr-triage/v1/...)
curl -s -X POST http://127.0.0.1:3000/v1/channels/github \
  -H 'content-type: application/json' \
  -H 'x-github-event: pull_request' \
  -d @fixtures/github/pull_request.opened.json
```

### Replay / Cursor events

```bash
npx --yes "@grok/agent-sdk@0.6.0" github replay okaybrah/cursor#1 --dir . --action opened
npx --yes "@grok/agent-sdk@0.6.0" serve --dir . --cursor-events --repo okaybrah/cursor
```

## Deploy

Do not deploy from this cloud agent. From your machine:

```bash
npx --yes "@grok/agent-sdk@0.6.0" deploy --dir . --dry-run
npx --yes "@grok/agent-sdk@0.6.0" deploy --dir . --cursor-events-repo okaybrah/cursor
```
