# Development and review rules

Read CONTRIBUTING.md and docs/security-development.md before changing code.

- Treat review findings as hypotheses: reproduce the defect, identify the security
  invariant, then write a regression that fails for that defect before fixing it.
  A compilation failure alone is not reproduction. Document rejected findings.
- For auth, files, concurrency, storage, networking or operations changes, state
  trust boundaries, resource budgets, failure behavior and acceptance tests first.
  Cover malformed input, simultaneous requests and dependency outages where relevant.
- Keep changes focused. Split unrelated security fixes; justify changes above 500
  lines in the PR. Coverage and fuzzing do not replace negative security tests.
- Run `xmake setup` to install both hooks. Use `xmake test` while editing. Commit,
  then run `xmake ci` on a clean checkout. Do not edit the checkout during CI.
  Any subsequent change needs a new commit and new CI evidence.
- Tests use disposable fixtures with synthetic data. Never copy real JWT keys,
  `.env`, credentials or backup contents into fixtures. Use private temporary
  directories (0700); do not dump environment variables or secrets to logs.
- Do not point tests at production or remote Docker. Do not mount the Docker
  socket, host root, production volumes or unrelated host directories in test
  containers. Cleanup only resources owned by the current test run; no global
  pruning or force deletion to work around a failed check.
- Commit acceptance scenarios and fixture setup. Commands or files left only in
  `/tmp` are supplementary evidence, not a reproducible regression suite.
- Publish verified evidence with `python3 scripts/dev/verification.py publish`.
  Never fabricate a successful status, reuse another SHA's result, disable a
  required check, skip hooks, self-approve or use an admin merge to finish a task.
- A security PR needs another person's approval of the latest push. Use
  `python3 scripts/dev/verification.py merge <PR>` only after approval. If review
  is pending, report the PR and remaining review; do not weaken protection.
- Distinguish code fixed, tests passed, deployment performed and production
  configuration verified. A local restore drill does not configure real backups.

These rules apply to humans and coding agents. Local CI executes trusted project
code with the developer's permissions; it is not an isolation boundary for untrusted PRs.
