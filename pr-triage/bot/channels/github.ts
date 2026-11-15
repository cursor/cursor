import {
  defaultGitHubAuth,
  defaultGitHubPullRequestDispatch,
  githubChannel,
  type GitHubCheckSuiteEvent,
  type GitHubEventContext,
} from "@grok/agent-sdk/channels/github";
import { watches } from "../lib/repos.js";

const MAX_DIFF_CHARS = 150_000;
const CI_FAILED_LABEL = "ci-failed";

export default githubChannel({
  botName: "pr-triage",
  // Repos come from serve/deploy: --repo / --cursor-events-repo.
  // watches() still gates hooks to bot/lib/repos.ts.
  cursorAccount: true,
  deliverReplies: false,
  onComment: (ctx, comment) => {
    if (!watches(ctx.repository.fullName)) {
      return null;
    }
    if (!comment.body.includes("@pr-triage")) {
      return null;
    }
    return { auth: defaultGitHubAuth(ctx) };
  },
  onPullRequest: (ctx, pr) => {
    if (!watches(ctx.repository.fullName) || pr.draft) {
      return null;
    }
    const dispatch = defaultGitHubPullRequestDispatch(ctx, pr);
    if (dispatch === null) {
      return null;
    }
    return {
      auth: dispatch.auth,
      title: `Review ${ctx.repository.fullName}#${pr.number}`,
      context: ["Read pr/diff.patch, then call submit_review once."],
      async workspaceFiles() {
        const octokit = await ctx.github.getOctokit();
        const pull = {
          owner: ctx.repository.owner,
          repo: ctx.repository.name,
          pull_number: pr.number,
        };
        const [meta, diff] = await Promise.all([
          octokit.rest.pulls.get(pull),
          octokit.rest.pulls.get({ ...pull, mediaType: { format: "diff" } }),
        ]);
        const text = typeof diff.data === "string" ? diff.data : "";
        return {
          "pr/diff.patch":
            text.length > MAX_DIFF_CHARS
              ? `${text.slice(0, MAX_DIFF_CHARS)}\n…[truncated]\n`
              : text,
          "pr/head.sha": meta.data.head.sha,
          "pr/meta.json": JSON.stringify(
            {
              title: meta.data.title,
              body: meta.data.body ?? "",
              author: meta.data.user?.login ?? "",
              base: meta.data.base.ref,
              head: meta.data.head.ref,
              url: meta.data.html_url,
            },
            null,
            2,
          ),
        };
      },
    };
  },
  onCheckSuite: (ctx, suite) => {
    if (!watches(ctx.repository.fullName)) {
      return null;
    }
    if (suite.conclusion !== "failure" || suite.pullRequests.length === 0) {
      return null;
    }
    return { task: () => labelCiFailure(ctx, suite) };
  },
});

async function labelCiFailure(
  ctx: GitHubEventContext,
  suite: GitHubCheckSuiteEvent,
): Promise<void> {
  const pr = suite.pullRequests[0];
  if (pr === undefined) {
    return;
  }
  const octokit = await ctx.github.getOctokit();
  await octokit.rest.issues.addLabels({
    owner: ctx.repository.owner,
    repo: ctx.repository.name,
    issue_number: pr.number,
    labels: [CI_FAILED_LABEL],
  });
}
