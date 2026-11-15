import { defineEval, includes } from "@grok/agent-sdk/evals";

const OPENED = [
  "GitHub pull request trigger: okaybrah/cursor#1",
  "Action: opened",
  "PR: https://github.com/okaybrah/cursor/pull/1",
  "Title: Admin flag",
  "Head: feat/admin-flag (aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa)",
  "Base: main",
  "Author: julio",
  "",
  "Read pr/diff.patch, then call submit_review once.",
].join("\n");

export default defineEval({
  tags: ["smoke"],
  cases: [
    {
      id: "finding",
      description: "A query-param admin grant is posted as a finding.",
      async test(t) {
        await t.send(OPENED, {
          workspaceFiles: {
            "pr/diff.patch": [
              "diff --git a/app/middleware/auth.ts b/app/middleware/auth.ts",
              '+if (req.query.admin === "true") { grantAdmin(req.user); }',
            ].join("\n"),
            "pr/head.sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            "pr/meta.json": JSON.stringify({
              title: "Admin flag",
              body: "",
              author: "julio",
              base: "main",
              head: "feat/admin-flag",
              url: "https://github.com/okaybrah/cursor/pull/1",
            }),
          },
        });
        t.succeeded();
        t.calledTool("submit_review");
        t.check(t.reply, includes(/auth|bypass|admin|finding|request/i));
      },
    },
    {
      id: "clean",
      description: "A docs-only change is posted as clean.",
      async test(t) {
        await t.send(OPENED.replace("Admin flag", "Docs"), {
          workspaceFiles: {
            "pr/diff.patch": ["diff --git a/README.md b/README.md", "+# API"].join(
              "\n",
            ),
            "pr/head.sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            "pr/meta.json": JSON.stringify({
              title: "Docs",
              body: "",
              author: "julio",
              base: "main",
              head: "docs",
              url: "https://github.com/okaybrah/cursor/pull/1",
            }),
          },
        });
        t.succeeded();
        t.calledTool("submit_review");
        t.check(t.reply, includes(/clean|no findings|approve|no .*issues/i));
      },
    },
  ],
});
