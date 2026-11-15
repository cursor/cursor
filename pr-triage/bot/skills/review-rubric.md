# Review rubric

Use this when reviewing a pull request for pr-triage.

## Read first

1. `pr/meta.json` — title, body, author, branches.
2. `pr/diff.patch` — only added or modified lines.

## Report

- Bugs in new code.
- Breaking side effects the author likely missed.
- Auth, secrets, or permission changes that widen access.
- Missing guards around user input that reaches a sink.

## Skip

- Style and formatting.
- Hypothetical future bugs with no path in this diff.
- Intentional breakage the PR states clearly when scope is tight.
- Inflating severity. Uncertain → low or omit.

## Finish

Call `submit_review` once. Empty `findings` means CLEAN and `verdict: "approve"`
or `"comment"`. Use `request_changes` only for high-severity issues that should
block merge.
