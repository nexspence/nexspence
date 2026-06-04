# Track A — golangci-lint Setup & Zero Findings — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Install and pin golangci-lint v2, add a tuned `.golangci.yml`, wire it into the Makefile and a new CI workflow, and fix every finding to zero — staged by linter group so each step is tractable.

**Architecture:** golangci-lint v2.12.2 installed via the Go module proxy (pinned `go install`, used identically in Makefile and CI — raw github.com is unreachable in this environment, the proxy is). Linters are enabled in **three staged phases** (standard → error-handling → style/security); after each phase we drive findings to zero and commit, so we never face a single wall of hundreds of findings. The fix work is discovery-driven: each phase first runs the linter to inventory real findings, then fixes them using the per-linter remediation recipes below.

**Tech Stack:** Go 1.26.3, golangci-lint v2.12.2 (config schema `version: "2"`), GitHub Actions.

**Branch:** Create and work on `track-a-golangci-lint` (do not commit directly to `main`).

**Note on TDD:** This is a tooling/config track, not feature code — there is no red-green test cycle. Each task's "verification" is a concrete command with expected output (linter exits 0, config parses, CI job green). The existing `go test` suite (474 tests) is the regression guard: it MUST still pass after every fix commit.

---

## Pre-flight

- [ ] **Step 0: Branch off main**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core
git checkout -b track-a-golangci-lint
git status   # expect: clean, on track-a-golangci-lint
```

---

## Task 1: Pin & install golangci-lint v2, fix the Makefile target

**Files:**
- Modify: `Makefile` (the `lint` target, ~line under `# ── Quality ──`)

The current target installs `github.com/golangci/golangci-lint/cmd/golangci-lint@latest` — that is the **v1** path and unpinned. Replace with the pinned **v2** path.

- [ ] **Step 1: Add a pinned version variable near the top of the Makefile**

Add after the `BUILD_DIR := ./bin` line:

```makefile
GOLANGCI_VERSION := v2.12.2
```

- [ ] **Step 2: Replace the `lint` target**

Find:

```makefile
.PHONY: lint
lint: ## Run golangci-lint
	@which golangci-lint > /dev/null || go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	golangci-lint run ./...
```

Replace with:

```makefile
.PHONY: lint
lint: ## Run golangci-lint (pinned version)
	@golangci-lint version 2>/dev/null | grep -q "$(patsubst v%,%,$(GOLANGCI_VERSION))" || \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	golangci-lint run ./...

.PHONY: lint-fix
lint-fix: ## Run golangci-lint with autofix
	golangci-lint run --fix ./...
```

- [ ] **Step 3: Install the pinned binary**

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
golangci-lint version
```

Expected: prints `golangci-lint has version 2.12.2 ...`. If `golangci-lint` is not on PATH, ensure `$(go env GOPATH)/bin` is on PATH for the session:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
golangci-lint version
```

- [ ] **Step 4: Commit**

```bash
git add Makefile
git commit -m "build(lint): pin golangci-lint to v2.12.2 in Makefile"
```

---

## Task 2: Add the `.golangci.yml` config — Phase 1 (standard linters only)

**Files:**
- Create: `.golangci.yml`

Start with only the `standard` default set (`errcheck, govet, ineffassign, staticcheck, unused`) plus the documented exclusions. We expand the linter set in Tasks 5 and 6.

- [ ] **Step 1: Create `.golangci.yml`**

```yaml
version: "2"

run:
  timeout: 5m
  tests: true

linters:
  default: standard
  exclusions:
    generated: lax
    paths:
      - frontend/node_modules
      - bin
      - third_party$
      - builtin$
    rules:
      # Test files: allow unchecked errors and weaker security posture
      - path: _test\.go
        linters:
          - errcheck
          - gosec

formatters:
  enable:
    - gofmt
    - goimports
  settings:
    goimports:
      local-prefixes:
        - github.com/nexspence-oss/nexspence
```

- [ ] **Step 2: Verify the config parses**

```bash
golangci-lint config verify
```

Expected: `Configuration is valid` (exit 0). If it reports an unknown field, fix it before continuing — do not proceed with an invalid config.

- [ ] **Step 3: Commit**

```bash
git add .golangci.yml
git commit -m "build(lint): add .golangci.yml (v2 schema, standard linters)"
```

---

## Task 3: Add the CI lint workflow

**Files:**
- Create: `.github/workflows/ci.yml`

There is no Go CI workflow yet (only `deploy-website.yml` and `release.yml`). Create one with a `lint` job. (Later tracks add `test` and `coverage` jobs to this same file.)

- [ ] **Step 1: Create `.github/workflows/ci.yml`**

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:

jobs:
  lint:
    name: golangci-lint
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.3'
          cache: true
      - name: Install golangci-lint (pinned, via Go proxy)
        run: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
      - name: Run golangci-lint
        run: golangci-lint run ./...
```

- [ ] **Step 2: Validate the workflow YAML locally**

```bash
python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/ci.yml')); print('yaml ok')"
```

Expected: `yaml ok`.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add golangci-lint job"
```

---

## Task 4: Phase 1 discovery + fix to zero (standard linters)

**Files:** discovered at runtime — fixes will touch `.go` files across `internal/` and `cmd/`.

- [ ] **Step 1: Inventory findings**

```bash
golangci-lint run ./... > /tmp/lint-phase1.txt 2>&1; echo "exit=$?"
grep -c '^' /tmp/lint-phase1.txt
# Per-linter histogram:
grep -oE '\(([a-z]+)\)$' /tmp/lint-phase1.txt | sort | uniq -c | sort -rn
```

Read `/tmp/lint-phase1.txt`. The histogram tells you how many findings per linter (errcheck, govet, staticcheck, ineffassign, unused).

- [ ] **Step 2: Fix findings using these recipes**

Apply **surgical** fixes only — no behavior changes, no unrelated refactoring (per CLAUDE.md §3). Re-run `golangci-lint run ./...` after each batch.

| Linter | Remediation recipe |
|--------|--------------------|
| **errcheck** | Handle the returned error. If genuinely ignorable, assign explicitly: `_ = w.Close()` or, for deferred closers, `defer func() { _ = f.Close() }()`. Never blanket-ignore a write/commit error. |
| **govet** | Usually struct-tag typos, shadowed vars, or `printf` format mismatches. Fix the actual issue (correct the verb, rename the shadow). |
| **staticcheck** | Follow the `SAxxxx` code: drop redundant nil checks, use `strings.EqualFold`, simplify `if x { return true } return false` → `return x`, remove unused struct fields, etc. Apply the suggested rewrite. |
| **ineffassign** | Remove the dead assignment, or use the value. E.g. `x, err := f(); ... err = g()` where the first `err` is never read → drop it. |
| **unused** | Delete the unused function/var/const **only if YOUR config surfaced it as dead** and it is not part of an exported API. If it's exported or part of an interface, add it to use or annotate. Prefer deletion of truly-dead unexported code. |

Auto-fixable subset (gofmt/goimports + some staticcheck) can be applied first:

```bash
golangci-lint run --fix ./...
```

Then fix the remainder by hand.

- [ ] **Step 3: Verify zero findings AND tests still pass**

```bash
golangci-lint run ./...; echo "lint-exit=$?"
go test -coverprofile=/tmp/cov_a.out ./... >/dev/null 2>&1; echo "test-exit=$?"
```

Expected: `lint-exit=0` and `test-exit=0`. (The rtk hook collapses `go test` stdout — rely on the exit code and, if needed, `go tool cover -func=/tmp/cov_a.out | tail -1`.)

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "fix(lint): resolve standard-linter findings (errcheck/staticcheck/govet/ineffassign/unused)"
```

---

## Task 5: Phase 2 — enable error-handling linters, fix to zero

**Files:**
- Modify: `.golangci.yml`
- Fixes across `.go` files

- [ ] **Step 1: Enable the error-handling linter group**

In `.golangci.yml`, add an `enable:` list and per-linter settings under `linters:`:

```yaml
linters:
  default: standard
  enable:
    - errorlint
    - bodyclose
    - sqlclosecheck
    - rowserrcheck
    - nilerr
    - nilnil
  settings:
    errorlint:
      errorf: true
      asserts: true
      comparison: true
  exclusions:
    generated: lax
    paths:
      - frontend/node_modules
      - bin
      - third_party$
      - builtin$
    rules:
      - path: _test\.go
        linters:
          - errcheck
          - gosec
```

- [ ] **Step 2: Verify config + inventory**

```bash
golangci-lint config verify
golangci-lint run ./... > /tmp/lint-phase2.txt 2>&1; echo "exit=$?"
grep -oE '\(([a-z]+)\)$' /tmp/lint-phase2.txt | sort | uniq -c | sort -rn
```

- [ ] **Step 3: Fix using these recipes**

| Linter | Remediation recipe |
|--------|--------------------|
| **errorlint** | Replace `err == sql.ErrNoRows` with `errors.Is(err, sql.ErrNoRows)`. Replace type assertions `err.(*MyErr)` with `errors.As(err, &target)`. Replace `fmt.Errorf("...%s", err)` wrapping with `%w`: `fmt.Errorf("...: %w", err)`. |
| **bodyclose** | Ensure every `http.Response.Body` is closed: `defer resp.Body.Close()` immediately after the error check. In proxy/replication code this is common. |
| **sqlclosecheck** | `defer rows.Close()` after every `Query`. |
| **rowserrcheck** | After iterating `rows.Next()`, add `if err := rows.Err(); err != nil { return ... }`. |
| **nilerr** | A function returned `nil` error when it found an error — fix the return to propagate the real error. |
| **nilnil** | A function returned `nil, nil` (both value and error nil) — return a sentinel error or a meaningful zero. If intentional and correct, add a targeted `//nolint:nilnil // <reason>`. |

- [ ] **Step 4: Verify zero + tests pass**

```bash
golangci-lint run ./...; echo "lint-exit=$?"
go test ./... >/dev/null 2>&1; echo "test-exit=$?"
```

Expected: both `=0`.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "fix(lint): resolve error-handling findings (errorlint/bodyclose/sqlclosecheck/rowserrcheck/nilerr)"
```

---

## Task 6: Phase 3 — enable style/security linters, fix to zero

**Files:**
- Modify: `.golangci.yml`
- Fixes across `.go` files

- [ ] **Step 1: Enable the style/security group**

Update the `enable:` and `settings:` blocks in `.golangci.yml` to the final set:

```yaml
linters:
  default: standard
  enable:
    # error-handling (from Phase 2)
    - errorlint
    - bodyclose
    - sqlclosecheck
    - rowserrcheck
    - nilerr
    - nilnil
    # style / security (Phase 3)
    - revive
    - gocritic
    - gosec
    - misspell
    - unconvert
    - nakedret
    - gocyclo
    - whitespace
    - usestdlibvars
  settings:
    errorlint:
      errorf: true
      asserts: true
      comparison: true
    gocyclo:
      min-complexity: 30
    nakedret:
      max-func-lines: 30
    misspell:
      locale: US
    revive:
      rules:
        - name: blank-imports
        - name: context-as-argument
        - name: error-return
        - name: error-strings
        - name: error-naming
        - name: increment-decrement
        - name: var-declaration
        - name: package-comments
          disabled: true
        - name: indent-error-flow
        - name: superfluous-else
        - name: unreachable-code
        - name: redefines-builtin-id
  exclusions:
    generated: lax
    paths:
      - frontend/node_modules
      - bin
      - third_party$
      - builtin$
    rules:
      - path: _test\.go
        linters:
          - errcheck
          - gosec
          - gocyclo
          - revive
```

- [ ] **Step 2: Verify config + inventory**

```bash
golangci-lint config verify
golangci-lint run ./... > /tmp/lint-phase3.txt 2>&1; echo "exit=$?"
grep -oE '\(([a-z]+)\)$' /tmp/lint-phase3.txt | sort | uniq -c | sort -rn
```

- [ ] **Step 3: Fix using these recipes**

| Linter | Remediation recipe |
|--------|--------------------|
| **revive** | Follow the named rule. `error-strings`: error messages must not be capitalized or end with punctuation. `indent-error-flow`/`superfluous-else`: remove `else` after a `return`. `context-as-argument`: `ctx` must be the first parameter. |
| **gocritic** | Apply the suggested diagnostic (e.g. `ifElseChain` → `switch`, `singleCaseSwitch` → `if`, `appendAssign`, `sloppyLen`). |
| **gosec** | Review each `Gxxx`. Real risks (e.g. `G401` weak crypto in security paths) → fix. False positives in non-security context (e.g. `G104` already handled, `G304` file path from validated config) → targeted `//nolint:gosec // <reason>` with a real justification. Do NOT blanket-disable gosec. |
| **misspell** | Accept the spelling correction. |
| **unconvert** | Remove the redundant type conversion. |
| **nakedret** | Add explicit return values in functions longer than 30 lines that use naked returns. |
| **gocyclo** | For functions over complexity 30, extract a helper or simplify. If the function is genuinely a large dispatch (e.g. a format router) and splitting hurts readability, add `//nolint:gocyclo // large protocol dispatch` with justification. |
| **whitespace** | Remove leading/trailing blank lines in blocks (autofixable: `--fix`). |
| **usestdlibvars** | Replace string literals like `"GET"` / `"200"` with `http.MethodGet` / `http.StatusOK`. |

Apply autofixes first, then hand-fix:

```bash
golangci-lint run --fix ./...
```

- [ ] **Step 4: Verify zero + full test suite + vet**

```bash
golangci-lint run ./...; echo "lint-exit=$?"
go vet ./...; echo "vet-exit=$?"
go test -race -count=1 ./... >/dev/null 2>&1; echo "test-exit=$?"
```

Expected: all three `=0`. The race detector run is the final regression gate.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "fix(lint): resolve style/security findings (revive/gocritic/gosec/misspell/nakedret/gocyclo)"
```

---

## Task 7: Documentation + changelog + finalize

**Files:**
- Modify: `NEXT_RELEASE.md`
- Modify: `CLAUDE.md` (Coding Standards section — note the pinned linter)

- [ ] **Step 1: Append to `NEXT_RELEASE.md`**

Add a bullet under the appropriate heading (follow the existing file's format):

```markdown
- **chore(lint):** Adopt golangci-lint v2.12.2 with a tuned `.golangci.yml` (standard + error-handling + style/security linters); wired into `make lint` and a new `CI` workflow `lint` job; resolved all findings to zero.
```

- [ ] **Step 2: Note the pinned tool in `CLAUDE.md`**

In the `## Coding Standards` section, change the Go line from:

```markdown
- Go: standard fmt, vet, golangci-lint
```

to:

```markdown
- Go: standard fmt, vet, golangci-lint v2.12.2 (pinned; config in `.golangci.yml`, run via `make lint`)
```

- [ ] **Step 3: Final full verification**

```bash
make lint; echo "make-lint-exit=$?"
go test -race -count=1 ./... >/dev/null 2>&1; echo "test-exit=$?"
git status
```

Expected: `make-lint-exit=0`, `test-exit=0`, working tree clean except the doc edits.

- [ ] **Step 4: Commit**

```bash
git add NEXT_RELEASE.md CLAUDE.md
git commit -m "docs: record golangci-lint v2 adoption (Track A complete)"
```

- [ ] **Step 5: Push and open PR (only when the user asks)**

Do NOT push or open a PR unless the user explicitly requests it. When they do:

```bash
git push -u origin track-a-golangci-lint
gh pr create --fill --base main
```

---

## Self-Review checklist (run before declaring Track A done)

- [ ] `make lint` exits 0
- [ ] `golangci-lint config verify` says valid
- [ ] All 474+ Go tests pass under `-race`
- [ ] `.golangci.yml` enables: standard set + errorlint, bodyclose, sqlclosecheck, rowserrcheck, nilerr, nilnil, revive, gocritic, gosec, misspell, unconvert, nakedret, gocyclo, whitespace, usestdlibvars
- [ ] CI workflow `.github/workflows/ci.yml` `lint` job present and YAML-valid
- [ ] No `//nolint` directive lacks a justification comment
- [ ] Every changed line traces to a lint finding or the tooling setup (no unrelated refactors — CLAUDE.md §3)
- [ ] `NEXT_RELEASE.md` updated (per memory: always before committing release-worthy work)
```
