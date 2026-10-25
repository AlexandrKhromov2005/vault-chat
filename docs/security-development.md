# Secure development: review of our process

The external review of `3ec755e` exposed defects that our tests and review process
had missed. PR #102 (`613a2b0`) fixed the confirmed findings, but its preparation
also exposed process failures. Passing the existing pipeline was insufficient.

| Observed failure | Required control | Evidence / limitation |
| --- | --- | --- |
| Password hashing and uploads had per-request limits without a global/per-user budget. | Define memory, concurrency, time and rate budgets before implementation; test simultaneous requests and exhaustion. | Behavioral regression tests required in the PR; coverage percentage alone cannot establish safety. |
| Auth, file names, roles, logout lifetime and dependency outages lacked explicit invariants. | Review identity normalization, session lifetime, detected file type/extension, least privilege and outage behavior. | Each changed boundary needs negative tests, not only a successful request. |
| A 110-file security PR mixed application and operations fixes and was merged by its author without independent approval. | Focused PRs; one other participant approves the latest push; stale approvals dismissed; administrators covered. | GitHub protection; no automatic admin override. The existing owner-only update ruleset remains. |
| Hooks were documented but not configured; GitHub required zero approvals. | `xmake setup` installs commit and push hooks; branch policy is stored in `.github/branch-protection.json`. | Check `git config --get core.hooksPath`; GitHub enforces review independently of local hooks. |
| Code could change while CI was running and an earlier green result could be mistaken for verification of the new code. | Run committed source in a separate detached checkout; check both checkouts and tool fingerprints afterward. | Receipt binds commit, tree, tools, log hash and time; new SHA, dirty source, changed tools/log or failed rerun invalidates it. Concurrent runs are rejected. |
| Old Go analyzer builds and Node 20 failed partway through validation; tool updates were improvised. | Preflight compatible pinned analyzers, Node 22.23.4, protobuf tooling and required crypto tools before starting fixtures. | `xmake doctor`; no `@latest` installation instructions. Receipts expire locally after 24 hours. |
| Integration tests could reuse the default developer Docker project. | A unique owned CI project, loopback ephemeral ports and disposable storage; cleanup only that project. | `deployments/docker-compose.ci.yml`; inherited test URLs are discarded. Fixed public fixture credentials are never production credentials. |
| Independent agent review found that filtering `DOCKER_CONFIG` could select a different daemon after preflight, and a stored Buildx builder could send builds elsewhere. | Resolve the effective endpoint through the Docker CLI, accept only an absolute Unix endpoint, pin it for all child commands and explicitly select the default builder. | Real isolated context/config regressions cover remote defaults, context changes and builder selection; the endpoint is part of the receipt. |
| Independent agent review found that SIGTERM bypassed Python cleanup and left detached checks running. | Convert SIGTERM to controlled cancellation, stop the check process group and finish owned-resource cleanup. | Subprocess regressions send real SIGTERM and check child exit, cleanup and absence of a successful receipt. |
| Some manual fixtures copied existing development keys and needed a later permission correction. | Generate synthetic secrets when needed; private temporary directories; never copy ignored working secrets. | CI checkout contains tracked source only; API/production environment variables are not forwarded. No JWT keys are needed by its infrastructure fixtures. |
| Live integration and full encrypted backup/restore acceptance relied partly on commands kept in `/tmp`. | Commit repeatable scenarios and setup for future changes at these boundaries. | Core local CI does **not** claim full live deployment or remote restore acceptance; those are separate required evidence for relevant changes. |
| Source changes, a successful isolated drill and actual production setup could be confused. | Report each state separately, with real evidence. | Production external backup destination and public age recipient still require operator configuration; a code merge cannot establish them. |

## Working sequence

1. State the invariant and attack/failure case. Add a regression and observe a
   behavioral failure. Do not treat a missing function or syntax error as proof.
2. Implement the smallest coherent correction. Use `xmake test`, `xmake lint` and
   targeted scenarios while editing. Keep fixtures synthetic and reproducible.
3. Commit the change. Run `xmake ci` with a clean checkout and leave it unchanged.
   Logs are retained in a private temporary directory; evidence is written under
   the common Git directory in `vault-verification/<full-SHA>.json` only on success.
4. The pre-push hook requires fresh evidence for **every pushed non-deleted tip**.
   Push the branch, publish the status with
   `python3 scripts/dev/verification.py publish`, then open a PR using the template.
   GitHub must have the commit before a status can be published. Include additional
   acceptance evidence where needed.
5. Another person reviews the latest push. Merge with
   `python3 scripts/dev/verification.py merge <PR>`; it checks the local receipt,
   exact PR head and approval, then uses the normal server-enforced merge endpoint.
   A refusal is a reason to fix/review the change, not add an admin fallback.

Core CI includes pinned-tool preflight, tracked-secret scan, gate regression tests,
protobuf lint, formatting, vet, lint, build, Go race tests against PostgreSQL and
scoped S3, web unit tests, coverage, deployment/crypto regression tests, monitoring
policy checks, every fuzz target for 10 seconds, and reachable vulnerability checks.
The 10 live gateway web scenarios and Go tests behind `-tags compose` are excluded;
the receipt records this. Full remote backup/restore and production configuration
are also outside this profile. For a change affecting these boundaries, add/run a
committed acceptance scenario and identify the result separately in the PR.

If the local Docker bridge cannot reach the Go module proxy during image builds,
Linux users can explicitly run `VAULT_CI_BUILD_NETWORK=host xmake ci`. Only the
trusted image build uses host networking; fixture containers keep their separate
bridge network and loopback ports. This option grants the build access to host
listeners, so it is opt-in, not an automatic retry. Its value is recorded in the
tool inventory; use the same setting for publish/push/merge evidence checks.
Individual fixture commands have a 15-minute deadline and core checks 30 minutes.
The validated Unix Docker endpoint is recorded in the tool inventory and pinned
for build, run and cleanup. `DOCKER_CONFIG` is preserved as an absolute path to
the existing client configuration; its contents are not copied. Image builds
explicitly use the default builder on that daemon, regardless of the stored
Buildx selection. Switching the current Docker context cannot retarget fixtures.

## Trust and recovery

Local hooks and JSON receipts are safeguards against mistakes. They are writable
by the developer, and `vault/local-ci` is an author-published status, not a trusted
attestation. GitHub does not enforce the local 24-hour receipt expiry or notice a
subsequent failed local rerun until a new status is published. Independent review
must inspect the evidence. A hosted or isolated trusted runner would be needed
for stronger guarantees against a malicious contributor; cloud CI is not currently
part of this project's workflow.

The scanner catches local config paths, embedded PEM private keys, age identities and selected
credential formats. It cannot recognize every secret. Review diffs and rotate any
credential that was exposed; deleting it from the latest tree is insufficient.

This runner executes trusted project code as the developer and uses the local
Docker daemon. A separate checkout and filtered environment do not sandbox code
or prevent it reading host credentials. Review untrusted changes before executing
them, or use a separately provisioned machine without personal credentials.

On failure no new receipt is produced, and a previous receipt for that SHA is
removed. Disposable containers/volumes are stopped in cleanup. Modified snapshots
are retained rather than force-deleted; inspect the printed path. SIGTERM initiates
cleanup; repeated SIGTERM is ignored until cleanup finishes, then the previous
handler is restored. A power loss or
SIGKILL can leave a project behind: inspect its exact `vault-ci-*` labels and clean
only that owned project. Do not run global Docker cleanup.

The branch policy can be reapplied by a repository administrator with
`gh api --method PUT repos/AlexandrKhromov2005/vault-chat/branches/main/protection --input .github/branch-protection.json`.
This does not modify the separate owner-only ruleset. Private vulnerability
reporting is enabled separately in repository settings.

References: [NIST SSDF 1.1](https://csrc.nist.gov/pubs/sp/800/218/final) on integrating
security and root-cause prevention into development, and
[GitHub branch protection](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches)
for approval and status-check semantics. This is a practical project policy, not
a claim of compliance certification.
