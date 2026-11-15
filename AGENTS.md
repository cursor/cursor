# AGENTS.md

This repository is the primary checkout in a multi-repo workspace. The runnable project is the MCP quickstart examples in the sibling `quickstart-resources` repository. `grok-prompts` and `plugins` are content checkouts and have no service to start.

## Cursor Cloud specific instructions

- `.cursor/install.sh` installs Node.js 24, Go, Rust, Ruby 3.4, and uv, then installs the quickstart examples' dependencies. Login shells pick the toolchains up from `/etc/profile.d/zz-dev-toolchains.sh`.
- From `../quickstart-resources`, `make check` lints every example, rebuilds the compiled ones, and runs the smoke tests.
- The weather servers speak MCP over stdio. `get_alerts` and `get_forecast` call the National Weather Service API. The smoke tests skip those tool-call checks when that API is unreachable.
- The example clients do not need `ANTHROPIC_API_KEY` for `make check`. The tool-loop tests use a local fake API.
