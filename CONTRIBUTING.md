# Contributing Guidelines

> Thank you for your interest in contributing! This document describes the repository workflow, change process, and code quality standards.

---

## Table of Contents

- [Quick Start](#quick-start)
- [Branching Strategy (GitHub Flow)](#branching-strategy-github-flow)
- [Commits](#commits)
- [Pull Requests](#pull-requests)
- [Test-Driven Development (TDD)](#test-driven-development-tdd)
- [Go Code Style](#go-code-style)
- [Testing](#testing)
- [Fuzzing](#fuzzing)
- [Issue Workflow](#issue-workflow)
- [CI / Local Verification](#ci--local-verification)
- [Pre-PR Checklist](#pre-pr-checklist)
- [Code Review](#code-review)
- [Security](#security)
- [Contacts](#contacts)

---

## Quick Start

### 1. Fork & Clone

```bash
# Fork the repository via GitHub UI, then:
git clone https://github.com/YOUR_USERNAME/PROJECT_NAME.git
cd PROJECT_NAME

# Add upstream
git remote add upstream https://github.com/ORG/PROJECT_NAME.git
```

### 2. Environment Setup

**Requirements:**
- Go `1.26+` (the `toolchain go1.27.3` line in `go.mod` is downloaded automatically with the default `GOTOOLCHAIN=auto`)
- `xmake` 2.8+ ([installation](https://xmake.io/#/guide/installation); on Ubuntu `apt install xmake`)
- Docker with Compose v2, for the integration tests in `xmake ci`
- Python 3.11+, OpenSSL, `age` and `age-keygen`, for mandatory deployment/crypto checks
- Node.js 22.23.4, for web tests
- Buf 1.47.2, for protobuf lint
- `golangci-lint` v2.14.0 built with Go 1.27.3
- `govulncheck` v1.9.0 built with Go 1.27.3 (pin the toolchain explicitly)
- `git` with `user.name` and `user.email` configured

Install the pinned Go analyzers into the ignored project tool directory:

```bash
mkdir -p .tools/bin
GOTOOLCHAIN=go1.27.3 GOBIN="$PWD/.tools/bin" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
GOTOOLCHAIN=go1.27.3 GOBIN="$PWD/.tools/bin" go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
GOTOOLCHAIN=go1.27.3 GOBIN="$PWD/.tools/bin" go install filippo.io/age/cmd/age@v1.3.1
GOTOOLCHAIN=go1.27.3 GOBIN="$PWD/.tools/bin" go install filippo.io/age/cmd/age-keygen@v1.3.1
export PATH="$PWD/.tools/bin:$PATH"
```

The hooks and verification commands prefer `.tools/bin`; individual test tasks
also use the exported PATH above. For Node 22.23.4, use the official
[release artifacts](https://nodejs.org/dist/v22.23.4/) and verify the archive against
`SHASUMS256.txt` before installing. Install the remaining tools
through their official distributions, then run `xmake doctor` to catch missing or
incompatible tools before expensive checks. This workflow requires local Unix
Docker, not a remote Docker context.
If the bridge cannot reach the Go proxy, see the explicit build-network option in
[secure development](docs/security-development.md); it is part of the CI evidence.

**Install dependencies and hooks:**

```bash
xmake setup
```

This will install:
- a `pre-commit` hook that scans staged Git blobs for secrets, then runs formatting, vet and lint
- a `pre-push` hook that requires current commit-bound CI evidence for every pushed tip
- project dependencies (`go mod download`)

### 3. Verify Everything Works

```bash
xmake test              # run all unit tests
xmake test-race         # tests with race detector
xmake fuzz              # fuzz tests (short run)
xmake lint              # run linters
xmake build             # build binary
```

---

## Branching Strategy (GitHub Flow)

We use **GitHub Flow** — a simple and predictable process:

```
main (protected branch)
  |
  ├── feature/add-auth          ← your feature branch
  ├── fix/race-condition
  ├── docs/api-examples
  └── refactor/db-layer
```

### Rules

| Rule | Description |
|------|-------------|
| `main` is always deployable | Only verified code lands in `main`. |
| Branch from `main` | Every change lives in its own feature branch. |
| Short-lived branches | Branch lifetime: max 3-5 days. Decompose the task if it takes longer. |
| PR → review → merge | Changes reach `main` only via Pull Request + code review. |

### Branch Naming

Format: `<type>/<short-description>`

| Prefix | When to use | Example |
|--------|-------------|---------|
| `feature/` | New functionality | `feature/jwt-auth` |
| `fix/` | Bug fix | `fix/conn-leak` |
| `docs/` | Documentation changes | `docs/api-spec` |
| `refactor/` | Refactoring without behavior change | `refactor/use-pool` |
| `test/` | Adding / fixing tests | `test/coverage-http` |
| `chore/` | Routine tasks (deps, CI) | `chore/update-linter` |

```bash
# Good
git checkout -b feature/user-registration

# Bad — too generic, no prefix
git checkout -b my-branch
```

### Sync with Upstream

```bash
git fetch upstream
git rebase upstream/main
# or, if already pushed:
git pull --rebase upstream main
```

---

## Commits

### Conventional Commits

All commits follow the [Conventional Commits](https://www.conventionalcommits.org/) specification:

```
<type>(<scope>): <short description>

[optional body]

[optional footer(s)]
```

### Commit Types

| Type | Description |
|------|-------------|
| `feat` | New feature |
| `fix` | Bug fix |
| `docs` | Documentation changes |
| `style` | Formatting, semicolons, etc. (no logic change) |
| `refactor` | Code refactoring |
| `test` | Adding / fixing tests |
| `chore` | Routine: dependencies, CI, configs |
| `perf` | Performance improvement |
| `ci` | CI/CD configuration changes |

### Examples

```bash
# Good
feat(auth): add JWT token validation
fix(db): close connection on context cancellation
docs(readme): add deployment instructions
refactor(cache): extract interface for testability

# Bad — unclear, no type
update stuff
fix bug
 wip
```

### Additional Rules

- **Language:** English (preferred for open-source compatibility).
- **Subject line:** max 72 characters.
- **Body:** explain **why**, not **what** — the diff shows "what".
- **One commit = one logical change.** Do not mix formatting with features.

---

## Pull Requests

### Creating a PR

1. Make sure your branch is synced with `main`.
2. Push to your fork: `git push origin feature/name`
3. Open a PR via GitHub UI to the **main repository**.

### PR Template

Fill out the template (located at `.github/pull_request_template.md`) when creating a PR:

```markdown
## Description
Brief description of changes and motivation.

## Related Issues
Closes #123, Relates to #456

## What Changed
- [ ] Added...
- [ ] Fixed...
- [ ] Updated...

## How to Verify
1. `xmake test` (all unit tests pass)
2. `xmake test-race` (no race conditions)
3. `xmake fuzz` (fuzz tests pass)
4. `xmake lint` (linters are clean)
5. Expected result: ...

## Screenshots / Logs
(if applicable)
```

### PR Requirements

| Requirement | Description |
|-------------|-------------|
| **One PR = one task** | Do not mix unrelated changes. |
| **Green local CI** | All local verification scripts must pass. |
| **Code review** | Minimum **1 approve** from another team member. |
| **No conflicts** | PR must be up to date with `main`. |
| **TDD** | Tests written before production code; commit history shows Red → Green → Refactor. |
| **Tests** | New code covered by unit tests; fuzz tests added for parsers / validators; existing tests not broken. |
| **Documentation** | `README.md`, `docs/`, or comments updated as needed. |

### Releases

Release tags use `vMAJOR.MINOR.PATCH` and point to verified commits in `main`.
Record user-visible changes under `Unreleased` in [CHANGELOG.md](CHANGELOG.md).
Follow [VERSIONING.md](VERSIONING.md) when publishing a release; published tags
are immutable. The repository uses one shared version for all services.

### Merge Strategy

- **Squash and merge** (recommended) — the entire PR history is squashed into a single commit in `main`.
- The commit in `main` must follow Conventional Commits.
- Delete the branch after merging.

---

## Test-Driven Development (TDD)

We use **TDD** as the primary development approach. This means: **test first — code second**.

### TDD Cycle: Red → Green → Refactor

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│  1. Red     │ ──▶ │  2. Green   │ ──▶ │ 3. Refactor │
│  Write a    │     │  Write the  │     │  Clean up   │
│  failing    │     │  minimum    │     │  code while │
│  test       │     │  code to    │     │  keeping    │
│             │     │  pass       │     │  tests green│
└─────────────┘     └─────────────┘     └──────┬──────┘
        ▲────────────────────────────────────────┘
```

### Steps

1. **Red** — write a test for functionality that does not exist yet. Run it — the test **must fail**. This proves the test works.
2. **Green** — write the **minimum** code to make the test pass. Do not think about architecture, cleanliness, or optimization — just make the test green.
3. **Refactor** — improve the code: remove duplication, use meaningful names, extract abstractions. After every change run the tests — they must stay green.

### TDD Example

**Task:** implement `CalculateDiscount(price, tier)`

**Step 1. Red — write the test**

```go
func TestCalculateDiscount_StandardTier(t *testing.T) {
    got, err := CalculateDiscount(100.0, "standard")
    require.NoError(t, err)
    require.Equal(t, 100.0, got) // standard — no discount
}
```

Run: `go test ./...` → **FAIL** (function does not exist yet).

**Step 2. Green — minimum code**

```go
func CalculateDiscount(price float64, tier string) (float64, error) {
    if tier == "standard" {
        return price, nil
    }
    return 0, errors.New("unknown tier")
}
```

Run: `go test ./...` → **PASS**.

**Step 3. Refactor — improve**

Add more cases via table-driven tests, extract magic numbers to constants, add validation.

### TDD Rules

| Rule | Description |
|------|-------------|
| **Test first** | No production code commit without a failing test before it. |
| **Small steps** | One test — one small feature. Do not write 10 tests at once. |
| **Commit progress** | Commit after each stage: `test: add test for X`, `feat: implement X`, `refactor: extract helper for X`. |
| **No production code without tests** | Exception: boilerplate (generated code), migrations, configs. |
| **Refactor in a separate commit** | `refactor:` — so the reviewer sees that logic did not change. |

### TDD Anti-Patterns

| Anti-Pattern | Why it is bad |
|--------------|---------------|
| **Wrote everything, then tests** | Not TDD. Tests become "self-check" instead of design tool. |
| **Tests fit the code, not code fits the tests** | Tests become brittle and useless. |
| **Steps too large** | Feedback is lost. If something breaks, you will not know where. |
| **Skipping refactoring** | Technical debt accumulates quickly. |

### TDD + Commits

Example commit history during TDD:

```
test(auth): add failing test for JWT validation
feat(auth): implement JWT token parsing
refactor(auth): extract claims validation to separate func
test(auth): add test for expired token edge case
fix(auth): handle expired tokens correctly
docs(auth): add godoc for auth package
```

---

## Go Code Style

We follow official Go standards with additional rules.

### Required Tools

| Tool | Command | Purpose |
|------|---------|---------|
| `gofmt` | `xmake fmt` | Automatic formatting |
| `go vet` | `xmake vet` | Static analysis |
| `golangci-lint` | `xmake lint` | Full linter suite |

### Key Rules

1. **Formatting:**
   - Use `gofmt` / `goimports`. Non-negotiable.
   - Max line length: ~120 characters (soft limit).

2. **Naming:**
   - Exported identifiers — `PascalCase`.
   - Unexported — `camelCase`.
   - Constants — `PascalCase` or `camelCase`.
   - Packages — short, lowercase, no underscore (`auth`, `db`, not `auth_utils`).

3. **Project Structure:**

   ```
   .
   ├── cmd/
   │   └── app/              ← entry points (main)
   ├── internal/
   │   ├── service/          ← business logic
   │   ├── repository/       ← DB / storage layer
   │   ├── handler/          ← HTTP / gRPC handlers
   │   └── config/           ← configuration
   ├── pkg/
   │   └── utils/            ← public utilities (if needed)
   ├── api/
   │   └── swagger/          ← API specifications
   ├── migrations/           ← SQL migrations
   ├── configs/              ← config files
   ├── docs/
   ├── xmake.lua             ← xmake build config
   ├── go.mod
   └── README.md
   ```

4. **Error Handling:**
   - Always check returned errors.
   - Wrap errors with context: `fmt.Errorf("failed to fetch user: %w", err)`.
   - Do not use `panic` in production code.

5. **Contexts:**
   - First function argument — `ctx context.Context`.
   - Pass `ctx` through; do not create `context.Background()` deep in the call stack.

6. **Interfaces:**
   - Define interfaces where they are **consumed**, not where they are **implemented** (Go-idiomatic).

### Clean Go Code Example

```go
package user

import (
    "context"
    "fmt"
    "time"
)

// Service provides user operations.
type Service struct {
    repo   Repository
    cache  Cache
    logger Logger
}

// NewService creates a new user service.
func NewService(repo Repository, cache Cache, logger Logger) *Service {
    return &Service{
        repo:   repo,
        cache:  cache,
        logger: logger,
    }
}

// GetByID returns a user by ID.
func (s *Service) GetByID(ctx context.Context, id string) (*User, error) {
    if id == "" {
        return nil, fmt.Errorf("user id is required")
    }

    user, err := s.cache.Get(ctx, id)
    if err == nil {
        return user, nil
    }

    user, err = s.repo.FindByID(ctx, id)
    if err != nil {
        return nil, fmt.Errorf("failed to find user %s: %w", id, err)
    }

    if cacheErr := s.cache.Set(ctx, id, user, 5*time.Minute); cacheErr != nil {
        s.logger.Warn("failed to cache user", "id", id, "error", cacheErr)
    }

    return user, nil
}
```

---

## Testing

> Before writing production code, read the [Test-Driven Development (TDD)](#test-driven-development-tdd) section.

### Coverage Requirements

- **Minimum:** 70% coverage in every business logic package (`internal/*/service`, `internal/*/handler`); `xmake test-coverage` checks each one.
- **Target:** 80%+.
- **Fuzzing:** all parsers, validators, and branching functions must have fuzz tests (see [Fuzzing](#fuzzing)).

### Running Tests

```bash
xmake test              # all unit tests
xmake test-race         # tests with race detector
xmake test-integration  # tests with race detector against PostgreSQL and S3 from Compose
xmake test-coverage     # coverage report
xmake fuzz              # fuzz tests (30s per test)
xmake fuzz-extended     # fuzz tests (10 min) — for nightly runs
```

### Test Writing Rules

- **TDD:** test first, code second. Exception: boilerplate, migrations, configs.
- Use `*_test.go` files in the same package (`package foo`) or external (`package foo_test`).
- Table-driven tests — preferred style.
- Generate mocks via `mockery` or `gomock`.
- Integration tests — in `tests/integration/`, require running services (Docker Compose).
- Fuzzing — mandatory for parsers, validators, crypto functions (see [Fuzzing](#fuzzing)).

### Table-Driven Test Example

```go
func TestValidateEmail(t *testing.T) {
    tests := []struct {
        name    string
        email   string
        wantErr bool
    }{
        {"valid", "user@example.com", false},
        {"no at", "userexample.com", true},
        {"empty", "", true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := ValidateEmail(tt.email)
            if tt.wantErr {
                require.Error(t, err)
                return
            }
            require.NoError(t, err)
        })
    }
}
```

---

## Fuzzing

Fuzzing is a mandatory part of testing. It helps find edge cases you missed when writing tests manually.

### When to Write Fuzz Tests

| Scenario | Mandatory? |
|----------|------------|
| Parsers (JSON, XML, CSV, query strings) | **Yes** |
| Input validators | **Yes** |
| Cryptographic functions, hashes | **Yes** |
| Functions with loops / recursion | **Yes** |
| Business logic with many branches | **Recommended** |
| Simple CRUD wrappers | No |

### How to Write Fuzz Tests

Go supports fuzzing natively via `testing.F`.

```go
// Fuzz test example for an email parser
func FuzzValidateEmail(f *testing.F) {
    // Seed corpus — valid and known cases
    f.Add("user@example.com")
    f.Add("a@b.co")
    f.Add("test.user+tag@domain.org")
    f.Add("")          // empty string
    f.Add("invalid")   // no @

    f.Fuzz(func(t *testing.T, email string) {
        err := ValidateEmail(email)

        // Verify the function does not panic
        // and returns a predictable result
        _ = err
    })
}
```

### Running Fuzzing

```bash
# Run with default duration (1 minute)
go test ./internal/validator -fuzz=FuzzValidateEmail

# Run for 5 minutes
go test ./internal/validator -fuzz=FuzzValidateEmail -fuzztime=5m

# Run for a specific iteration count
go test ./internal/validator -fuzz=FuzzValidateEmail -fuzztime=100000x

# Every fuzz target in the project, one after another
xmake fuzz              # 30s per target
xmake fuzz -t 2m        # another duration per target
```

### Fuzz Test Rules

| Rule | Description |
|------|-------------|
| **Seed corpus** | Always add known cases via `f.Add()`. This guarantees basic scenarios are checked. |
| **Do not check exact results** | The fuzzer generates random data. Check invariants: "does not panic", "does not leak", "returns error on garbage". |
| **Check invariants** | For example: if `Parse` returned a nil error, then `Serialize(Parse(x)) == x`. |
| **Skip known limitations** | If a function does not support strings > 10KB, use `t.Skip()` in the fuzz test. |
| **Corpus in repository** | Save discovered crash cases to `testdata/fuzz/<name>/<hash>` — they will run as regular tests. |

### Example: Fuzzing with Invariant Checks

```go
// FuzzSerializeParse verifies that serialize(parse(x)) == x for valid data
func FuzzSerializeParse(f *testing.F) {
    f.Add(`{"name":"test","age":30}`)
    f.Add(`{"name":"","age":0}`)

    f.Fuzz(func(t *testing.T, input string) {
        // Skip oversized input
        if len(input) > 1024*1024 {
            t.Skip("input too large")
        }

        obj, err := Parse(input)
        if err != nil {
            // Invalid JSON is fine, but it must not panic
            return
        }

        // Invariant: serialization round-trip yields equivalent object
        serialized, err := Serialize(obj)
        require.NoError(t, err)

        obj2, err := Parse(serialized)
        require.NoError(t, err)
        require.Equal(t, obj, obj2)
    })
}
```

---

## CI / Local Verification

**All verification runs locally on each developer's machine.** We do not use cloud runners — run everything before pushing.

### Why Local CI?

- No cloud infrastructure costs.
- Immediate feedback — no waiting for queue.
- Same environment for all team members.
- Works offline.

### Pre-Push Verification

Before every push, run the full local verification suite:

```bash
xmake ci              # run the complete local CI pipeline (see below);
                      # it stops at the first failure with a non-zero exit code
```

### What `xmake ci` Runs

| Step | Command | Description |
|------|---------|-------------|
| Preflight | `xmake doctor` | Compatible pinned analyzers, Node, crypto and local Docker |
| Secrets | `xmake secret-scan` | Tracked Git blobs; findings never print secret values |
| Verification gates | `xmake dev-test` | Regression tests of stale/failed evidence and isolation |
| Protobuf | `xmake proto` | Buf lint |
| Format check | `xmake fmt-check` | Verify `gofmt` / `goimports` compliance |
| Vet | `xmake vet` | `go vet` static analysis |
| Lint | `xmake lint` | `golangci-lint` full suite |
| Build | `xmake build` | Every package compiles |
| Tests | `xmake test-integration` | All tests with race detector, including PostgreSQL and S3 integration tests and the web client's tests |
| Coverage | `xmake test-coverage` | Each `service` and `handler` package >= 70% |
| Fuzz regression | `xmake fuzz -t 10s` | Every fuzz target for 10s |
| Vulnerabilities | `xmake vuln` | `govulncheck`: no reachable known vulnerabilities |

`xmake test-integration` uses `VAULT_CHAT_TEST_DATABASE_URL`, `VAULT_CHAT_TEST_CHAT_DATABASE_URL`,
`VAULT_CHAT_TEST_MEDIA_DATABASE_URL` and `VAULT_CHAT_TEST_MEDIA_S3_ENDPOINT` when they are all set.
It fails if any endpoint is absent. Use `xmake ci` to provision disposable fixtures
in a unique Docker project with ephemeral loopback ports; it never reuses the
normal development Compose project or inherited test URLs.

Commit first, then run `xmake ci` on a clean checkout. It tests a separate checkout
of that exact commit without copying ignored `.env` or secrets, and writes local
evidence only when source, tools and checks remain valid. Do not edit during CI.
Use `xmake test` for uncommitted iterations. Push requires evidence less than 24
hours old; publish the status with `python3 scripts/dev/verification.py publish`.
The profile excludes the 10 live gateway web tests, Go `-tags compose` tests, a
full remote backup/restore drill and production setup. Provide separate committed
acceptance scenarios for changes affecting those boundaries. See
[secure development](docs/security-development.md) for the trust limitations.

**PRs with failing local CI are not reviewed and not merged.**

### Individual Commands

```bash
xmake fmt             # auto-format code
xmake fmt-check       # check formatting without changes
xmake vet             # go vet
xmake lint            # golangci-lint
xmake test            # unit tests
xmake test-race       # unit tests + race detector
xmake test-integration # tests + race detector + PostgreSQL and S3 integration tests
xmake test-coverage   # coverage report
xmake fuzz            # fuzz tests (30s)
xmake fuzz-extended   # fuzz tests (10 min)
xmake vuln            # govulncheck
xmake build           # build every package
xmake ci              # full local pipeline (see the table above)
```

### Git Hooks

`xmake setup` installs both hooks via `core.hooksPath=.githooks`. The commit hook
checks staged secrets and fast Go checks. The push hook validates the exact pushed
commits against local CI evidence; it does not start CI automatically. Missing,
failed, expired or mismatched evidence requires a new `xmake ci` run.

Do not bypass hooks or protection to finish a task. Another participant must
approve the latest push; new pushes dismiss stale approvals. The guarded merge
command is `python3 scripts/dev/verification.py merge <PR>` and has no admin
fallback. Local evidence and the author-published status do not replace review.

---

## Pre-PR Checklist

```markdown
## Pre-PR Checklist

- [ ] TDD: tests written BEFORE production code (except boilerplate)
- [ ] Code compiles: `xmake build`
- [ ] All unit tests pass: `xmake test`
- [ ] Race detector tests pass: `xmake test-race`
- [ ] Fuzz tests pass: `xmake fuzz`
- [ ] Coverage >= 70%: `xmake test-coverage`
- [ ] Linters pass: `xmake lint`
- [ ] Full local CI passes: `xmake ci`
- [ ] No `panic`, `log.Fatal` outside `main`
- [ ] Errors are handled and wrapped
- [ ] New code is covered by tests (unit + fuzz where applicable)
- [ ] Documentation updated (README, comments, API spec)
- [ ] PR follows the template
- [ ] No conflicts with `main`
```

---

## Code Review

### For PR Authors

- Describe **what** was done and **why**.
- If the PR is large (>500 lines) — split it into several.
- Reply to comments, resolve after fixing.
- Do not resolve others' comments — let the reviewer do it.

### For Reviewers

- **Be constructive.** Explain "why", not just "what".
- **Priorities:**
  1. Correctness and security (blocking comments)
  2. Architecture and design
  3. Performance
  4. Style and naming (non-critical, use `nit:`)
- **Speed:** aim to review within 24 hours.
- **Approve** — if you are willing to take responsibility for this code in `main`.

### Comment Prefixes

| Prefix | Meaning | Must fix? |
|--------|---------|-----------|
| (no prefix) | Regular comment | Yes |
| `nit:` | Nitpick, style | No, but preferred |
| `question:` | Question for understanding | Discuss |
| `suggestion:` | Concrete proposal | Consider |
| `blocking:` | Critical, merge blocked | Yes |

---

## Security

- Never commit secrets: tokens, passwords, private keys.
- Use `.env.example` instead of real `.env` files.
- Check reachable dependency vulnerabilities: `xmake vuln`.
- Follow [secure development](docs/security-development.md) and the PR security acceptance template.
- If you find a vulnerability — contact maintainers privately via [SECURITY.md](SECURITY.md), do not open a public Issue.

---

## Contacts

- General questions — GitHub Discussions
- Bugs and features — GitHub Issues
- Private security reports — [SECURITY.md](SECURITY.md)

---

## License

By contributing, you agree that your code will be distributed under the project's license (see [LICENSE](LICENSE)).

---

> **Remember:** code is read more often than it is written. Invest time in clarity and documentation — it is respect for your teammates and future contributors.
