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
- Go `1.23+`
- `xmake` ([installation](https://xmake.io/#/guide/installation))
- `golangci-lint` ([installation](https://golangci-lint.run/usage/install/))
- `git` with `user.name` and `user.email` configured

**Install dependencies and hooks:**

```bash
xmake setup
```

This will install:
- `pre-commit` hooks (`gofmt`, `go vet`, `golangci-lint`)
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

- **Minimum:** 70% coverage in business logic packages (`internal/service/`, `internal/handler/`).
- **Target:** 80%+.
- **Fuzzing:** all parsers, validators, and branching functions must have fuzz tests (see [Fuzzing](#fuzzing)).

### Running Tests

```bash
xmake test              # all unit tests
xmake test-race         # tests with race detector
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

# CI — short fuzzing for regression
xmake fuzz          # alias: go test ./... -fuzz=. -fuzztime=30s
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
xmake ci              # run the complete local CI pipeline:
                      #   gofmt, go vet, golangci-lint,
                      #   unit tests (with race), fuzz tests, build
```

### What `xmake ci` Runs

| Step | Command | Description |
|------|---------|-------------|
| Format check | `xmake fmt-check` | Verify `gofmt` / `goimports` compliance |
| Vet | `xmake vet` | `go vet` static analysis |
| Lint | `xmake lint` | `golangci-lint` full suite |
| Unit tests | `xmake test-race` | All tests with race detector |
| Fuzz regression | `xmake fuzz` | Fuzz tests (30s each) |
| Coverage | `xmake test-coverage` | Ensure >= 70% |
| Build | `xmake build` | Binary compiles cleanly |

**PRs with failing local CI are not reviewed and not merged.**

### Individual Commands

```bash
xmake fmt             # auto-format code
xmake fmt-check       # check formatting without changes
xmake vet             # go vet
xmake lint            # golangci-lint
xmake test            # unit tests
xmake test-race       # unit tests + race detector
xmake test-coverage   # coverage report
xmake fuzz            # fuzz tests (30s)
xmake fuzz-extended   # fuzz tests (10 min)
xmake build           # build binary
xmake ci              # full local pipeline (all of the above)
```

### Git Pre-Push Hook

The `xmake setup` command installs a pre-push hook that runs `xmake ci`. You can skip it in emergencies (not recommended):

```bash
git push --no-verify   # skip hooks — use only in real emergencies
```

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
- Check dependencies: `go list -m all | nancy sleuth`.
- If you find a vulnerability — contact maintainers privately via [SECURITY.md](SECURITY.md), do not open a public Issue.

---

## Contacts

- General questions — GitHub Discussions
- Bugs and features — GitHub Issues
- Urgent / private — Telegram / Discord (links in maintainer profiles)

---

## License

By contributing, you agree that your code will be distributed under the project's license (see [LICENSE](LICENSE)).

---

> **Remember:** code is read more often than it is written. Invest time in clarity and documentation — it is respect for your teammates and future contributors.
