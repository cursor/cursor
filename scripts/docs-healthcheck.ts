#!/usr/bin/env node
/**
 * Local one-shot: ask a Cursor agent to audit this repo's public community docs.
 *
 * Why SDK here: the valuable surface in this workspace is README + SECURITY +
 * issue templates — not application code. Agent.prompt runs against the local
 * checkout (including uncommitted edits) and returns a concrete checklist you
 * can act on before merging doc changes.
 *
 * Usage:
 *   export CURSOR_API_KEY=cursor_...
 *   npm run docs:health
 *   npm run docs:health -- "Also check whether the forum link is still current"
 */
import { writeFile } from "node:fs/promises";
import { Agent, Cursor, CursorAgentError } from "@cursor/sdk";

const apiKey = process.env.CURSOR_API_KEY?.trim();
if (!apiKey) {
  const auth = await Cursor.auth.status();
  if (auth.status !== "logged-in") {
    console.error(
      "No credentials. Export CURSOR_API_KEY (https://cursor.com/dashboard/cloud-agents) or run Cursor.auth.login() once."
    );
    process.exit(1);
  }
  console.error(`[docs:health] using stored SDK login (${auth.email ?? "logged-in"})`);
}

const extra = process.argv.slice(2).join(" ").trim();

const prompt = `You are auditing the public community docs in this repository (cwd).

Read these files thoroughly:
- README.md
- SECURITY.md
- .github/ISSUE_TEMPLATE/new-issue.md

Produce a concise health report with:
1. **Routing** — Does GitHub clearly push bugs/feedback to forum.cursor.com, and security reports to security-reports@cursor.com (not public issues)?
2. **Gaps** — Missing sections a visitor would expect (status page, support channels, contributing, response SLAs, etc.). Only flag real gaps for a minimal public/community repo — do not invent a product docs site.
3. **Concrete edits** — Up to 5 specific copy/structure changes, each with the target file path and the exact replacement or addition suggested.
4. **Severity** — Tag each finding as blocker / should-fix / nit.

Rules:
- Prefer short bullets over prose.
- Do not rewrite the whole files unless a rewrite is clearly justified.
- Do not invent policy Cursor has not stated; flag uncertainty instead.
${extra ? `\nAdditional focus from the operator:\n${extra}\n` : ""}
Write the full report as markdown.`;

async function main() {
  console.error("[docs:health] starting local Agent.prompt…");

  try {
    const result = await Agent.prompt(prompt, {
      // Prefer explicit env key; otherwise SDK falls back to Cursor.auth.login() store.
      ...(apiKey ? { apiKey } : {}),
      model: { id: "composer-2" },
      local: {
        cwd: process.cwd(),
        // Don't pull ambient IDE/team settings into a CI/script context.
        settingSources: [],
      },
    });

    console.error(
      `[docs:health] status=${result.status} run=${result.id ?? "n/a"} durationMs=${result.durationMs ?? "n/a"}`
    );

    if (result.status !== "finished") {
      console.error(`[docs:health] run ended as ${result.status}`);
      if (result.result) console.error(result.result);
      process.exit(2);
    }

    const report = (result.result ?? "").trim() || "(no output)";
    await writeFile("docs-health-report.md", `${report}\n`, "utf8");
    console.log(report);
    console.error("\n[docs:health] wrote docs-health-report.md");
  } catch (err) {
    if (err instanceof CursorAgentError) {
      console.error(
        `[docs:health] startup failed: ${err.message} (retryable=${err.isRetryable})`
      );
      process.exit(err.isRetryable ? 75 : 1);
    }
    throw err;
  }
}

main();
