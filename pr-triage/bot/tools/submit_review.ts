import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { parseGitHubPrContinuationKey } from "@grok/agent-sdk/channels/github";
import { defineTool } from "@grok/agent-sdk/tools";
import * as z from "zod/v4";

const SHA = /^[0-9a-f]{40}$/i;

const finding = z.object({
  severity: z.enum(["high", "medium", "low"]),
  file: z.string().min(1).max(240),
  line: z.number().int().positive(),
  title: z.string().min(1).max(160),
  detail: z.string().min(1).max(280),
});

type Finding = z.infer<typeof finding>;

export default defineTool({
  description:
    "Post the review decision on this session's pull request. Call once. Empty findings = CLEAN.",
  effect: "write",
  inputSchema: z.object({
    verdict: z.enum(["approve", "comment", "request_changes"]),
    summary: z
      .string()
      .min(1)
      .max(500)
      .describe("One or two sentences explaining the verdict."),
    findings: z
      .array(finding)
      .describe("Issues in the diff. Empty when the change looks clean."),
  }),
  dryRunResult: { posted: false, reason: "dry-run" },
  async execute({ verdict, summary, findings }, ctx) {
    if (ctx.session.purpose === "eval") {
      return { posted: false, reason: "eval", verdict, findings: findings.length };
    }
    const ref = parseGitHubPrContinuationKey(ctx.session.continuationKey ?? "");
    if (ref === undefined) {
      throw new Error("submit_review requires a GitHub pull request session");
    }

    let headSha = "";
    try {
      headSha = (await readFile(join(ctx.workspaceDir, "pr/head.sha"), "utf8")).trim();
    } catch {
      headSha = "";
    }
    if (headSha !== "" && !SHA.test(headSha)) {
      throw new Error("invalid pr/head.sha");
    }

    const event =
      verdict === "approve"
        ? "APPROVE"
        : verdict === "request_changes"
          ? "REQUEST_CHANGES"
          : "COMMENT";

    const octokit = await ctx.host.github.getOctokit();
    const response = await octokit.rest.pulls.createReview({
      owner: ref.owner,
      repo: ref.repo,
      pull_number: ref.number,
      ...(headSha !== "" ? { commit_id: headSha } : {}),
      event,
      body: reviewBody(verdict, summary, findings),
    });

    return {
      posted: true,
      pr: `${ref.owner}/${ref.repo}#${ref.number}`,
      url: response.data.html_url,
      verdict,
      findings: findings.length,
    };
  },
});

function flatten(value: string): string {
  return value.replace(/\s+/g, " ").trim();
}

function reviewBody(
  verdict: "approve" | "comment" | "request_changes",
  summary: string,
  findings: Finding[],
): string {
  const label =
    findings.length === 0
      ? "CLEAN"
      : verdict === "request_changes"
        ? "REQUEST CHANGES"
        : "FINDINGS";
  const lines = [
    "## pr-triage review",
    "",
    `**Verdict:** ${label}`,
    "",
    flatten(summary),
  ];
  if (findings.length > 0) {
    lines.push("", "### Findings");
    for (const item of findings) {
      lines.push(
        `- **${item.severity}** \`${flatten(item.file)}:${item.line}\` — ${flatten(item.title)}`,
      );
      lines.push(`  ${flatten(item.detail)}`);
    }
  }
  return lines.join("\n");
}
