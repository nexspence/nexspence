# Automated Release Notes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the manual `NEXT_RELEASE.md` → shell → `--release-notes` chain with GoReleaser-native changelog generation, svu-computed versions, and a PR-title lint — so GitHub release bodies are always populated automatically.

**Architecture:** GoReleaser builds the release body itself from `git log` (Conventional Commits, grouped), with the static Docker block in `release.footer`. A new manually-triggered `tag.yml` workflow uses `svu` to compute the next `vX.Y.Z` and pushes the tag (triggering `release.yml`). A `pr-title.yml` workflow enforces Conventional-Commit PR titles. The old NEXT_RELEASE.md machinery is deleted.

**Tech Stack:** GoReleaser v2 (`changelog`/`release.footer`), `svu` (caarlos0/svu/v2), `amannn/action-semantic-pull-request`, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-06-09-automated-release-notes-design.md`

**Working branch:** `ci/automated-release-notes` (already created; spec committed there as `721c745`).

**Reference facts (verified in the repo):**
- `.goreleaser.yaml`: `changelog:\n  disable: true` at lines 75-76; `release:` block at 78-88 (owner/name `nexspence/nexspence`, `mode: replace`, `target_commitish: main`, `extra_files` glob `./release-assets/nexspence-*.zip`).
- `.github/workflows/release.yml`: step `Build release notes` at lines 125-169; `Run GoReleaser` at 182-188 with `args: release --clean --release-notes=release-notes.md`; `Reset changelog for next release` at 212-226. The `Checkout` step (line 20) uses `fetch-depth: 0` + `token: ${{ secrets.RELEASE_PAT }}`. `Set up Go`/`Set up Node` (170-180) stay (GoReleaser's before-hook needs them).
- `.gitignore`: `release-assets/` (line 69) and `release-notes.md` (line 70).
- `go.mod`: `go 1.26.3`.
- Release repo is **`nexspence/nexspence`** (public), built from `nexspence-core`; auth via `secrets.RELEASE_PAT`.

---

## File Structure

**Modify:**
- `.goreleaser.yaml` — replace `changelog.disable` with native changelog config; add `release.footer`.
- `.github/workflows/release.yml` — remove `Build release notes` + `Reset changelog` steps; drop `--release-notes` from GoReleaser args.
- `.gitignore` — remove the `release-notes.md` line.

**Create:**
- `.github/workflows/tag.yml` — svu version computation + tag push.
- `.github/workflows/pr-title.yml` — Conventional-Commit PR-title lint.

**Delete:**
- `NEXT_RELEASE.md`.

---

## Task 1: GoReleaser native changelog + footer

**Files:**
- Modify: `.goreleaser.yaml`

- [ ] **Step 1: Replace the `changelog` block**

In `.goreleaser.yaml`, replace exactly:
```yaml
changelog:
  disable: true
```
with:
```yaml
changelog:
  use: git              # parse local nexspence-core git log; cross-repo safe
  sort: asc
  filters:
    exclude:
      - '^chore:'
      - '^ci:'
      - '^test:'
      - '^build:'
      - '^style:'
      - '^Merge '
  groups:
    - title: "✨ Features"
      regexp: '^.*?feat(\(.+\))??!?:.+$'
      order: 0
    - title: "🐛 Bug Fixes"
      regexp: '^.*?fix(\(.+\))??!?:.+$'
      order: 1
    - title: "📚 Documentation"
      regexp: '^.*?docs(\(.+\))??!?:.+$'
      order: 2
    - title: "🔧 Others"
      order: 999
```

- [ ] **Step 2: Add `release.footer`**

In `.goreleaser.yaml`, the `release:` block currently ends with:
```yaml
  extra_files:
    - glob: ./release-assets/nexspence-*.zip
```
Append a `footer:` key right after it (same indentation level as `extra_files`, i.e. 2 spaces), so the block becomes:
```yaml
  extra_files:
    - glob: ./release-assets/nexspence-*.zip
  footer: |
    ### 🐳 Docker

    | | |
    |---|---|
    | **Image** | `ghcr.io/nexspence/nexspence:{{ .Tag }}` |
    | **Platforms** | linux/amd64, linux/arm64 |

    ```bash
    docker pull ghcr.io/nexspence/nexspence:{{ .Tag }}
    ```

    #### Quick start

    Download `nexspence-{{ .Tag }}.zip`, extract it, rename
    `config.yaml.example` → `config.yaml`, and set the admin password and JWT secret.

    | Mode | Command |
    |------|---------|
    | Single-node | `docker compose up -d` |
    | Single-node + Monitoring | `docker compose --profile monitoring up -d` |
    | Single-node + Keycloak SSO | `OIDC_ENABLED=true docker compose --profile keycloak up -d` |
    | HA (2 nodes + nginx) | `docker compose -f docker-compose.ha.yml up -d` |

    **Web UI:** single-node → `http://localhost:8081` · HA → `http://localhost:8080`

    Native install (deb/rpm/macOS/Windows): see
    [docs/install-local.md](https://github.com/nexspence/nexspence/blob/main/docs/install-local.md).
```

- [ ] **Step 3: Validate the config**

Run:
```bash
export PATH="$(go env GOPATH)/bin:$PATH"
command -v goreleaser >/dev/null || go install github.com/goreleaser/goreleaser/v2@latest
goreleaser check
```
Expected: `1 configuration file(s) validated` with no errors. If it reports a schema error in `changelog` or `release.footer`, fix the YAML to satisfy the installed GoReleaser v2 while preserving intent, and note the change.

- [ ] **Step 4: Verify a snapshot still builds with the new config**

Run (builds the frontend via the before-hook, then cross-compiles — several minutes):
```bash
export PATH="$(go env GOPATH)/bin:$PATH"
goreleaser release --snapshot --clean >/tmp/gr.log 2>&1; echo "exit=$?"
ls dist/*.deb dist/*.rpm dist/checksums.txt 2>&1 | tail -5
```
Expected: `exit=0`; `dist/` contains the `.deb`, `.rpm`, and `checksums.txt` (the changelog grouping itself is exercised on the live release per the spec's acceptance criteria — snapshot just proves the config still builds).

- [ ] **Step 5: Commit**

```bash
git add .goreleaser.yaml
git commit -m "build(goreleaser): generate release body from git changelog + footer"
```

---

## Task 2: Clean up release.yml (remove manual notes machinery)

**Files:**
- Modify: `.github/workflows/release.yml`

- [ ] **Step 1: Drop the `--release-notes` flag from the GoReleaser step**

In `.github/workflows/release.yml`, replace exactly:
```yaml
          args: release --clean --release-notes=release-notes.md
```
with:
```yaml
          args: release --clean
```

- [ ] **Step 2: Delete the `Build release notes` step**

This step is a long shell block (begins `- name: Build release notes`, ends right before `- name: Set up Go`). Delete it deterministically by anchors:
```bash
python3 - <<'PY'
import re
p='.github/workflows/release.yml'
s=open(p).read()
# Remove from the "Build release notes" step start up to (but not including) "Set up Go"
pat=re.compile(r'      - name: Build release notes\n(?:.*\n)*?(?=      - name: Set up Go\n)')
new,n=pat.subn('', s)
assert n==1, f"expected 1 match, got {n}"
open(p,'w').write(new)
print("removed Build release notes step")
PY
```
Expected: `removed Build release notes step`.

- [ ] **Step 3: Delete the `Reset changelog for next release` step**

This is the last step in the file. Delete it by anchor:
```bash
python3 - <<'PY'
import re
p='.github/workflows/release.yml'
s=open(p).read()
# Remove from "Reset changelog for next release" to end of file
pat=re.compile(r'\n      - name: Reset changelog for next release\n(?:.*\n?)*\Z')
new,n=pat.subn('\n', s)
assert n==1, f"expected 1 match, got {n}"
open(p,'w').write(new)
print("removed Reset changelog step")
PY
```
Expected: `removed Reset changelog step`.

- [ ] **Step 4: Verify YAML validity and the invariants**

Run:
```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml')); print('YAML OK')"
echo "--- these should print NOTHING ---"
grep -nE 'Build release notes|Reset changelog|release-notes\.md' .github/workflows/release.yml || echo "clean: no notes machinery remains"
echo "--- these should still be present ---"
grep -nE 'name: Run GoReleaser|name: Sync docs|args: release --clean' .github/workflows/release.yml
```
Expected: `YAML OK`; `clean: no notes machinery remains`; and the GoReleaser + Sync-docs steps + `args: release --clean` are still present.

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci: drop manual NEXT_RELEASE.md notes machinery from release workflow"
```

---

## Task 3: svu version + tag workflow

**Files:**
- Create: `.github/workflows/tag.yml`

- [ ] **Step 1: Create `.github/workflows/tag.yml`**

```yaml
name: Tag Release

on:
  workflow_dispatch:
    inputs:
      dry_run:
        description: "Show the next version without creating a tag"
        type: boolean
        default: false

jobs:
  tag:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0          # svu needs full history + tags
          token: ${{ secrets.RELEASE_PAT }}   # PAT so the tag push triggers release.yml

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Install svu
        run: go install github.com/caarlos0/svu/v2@latest

      - name: Compute next version
        id: ver
        run: |
          NEXT="$(svu next)"
          echo "next=$NEXT" >> "$GITHUB_OUTPUT"
          echo "Next version: $NEXT"
          echo "### Next version: \`$NEXT\`" >> "$GITHUB_STEP_SUMMARY"

      - name: Create and push tag
        if: ${{ !inputs.dry_run }}
        run: |
          git config user.name  "github-actions[bot]"
          git config user.email "github-actions[bot]@users.noreply.github.com"
          git tag -a "${{ steps.ver.outputs.next }}" -m "Release ${{ steps.ver.outputs.next }}"
          git push origin "${{ steps.ver.outputs.next }}"
```

- [ ] **Step 2: Verify YAML validity**

Run:
```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/tag.yml')); print('YAML OK')"
```
Expected: `YAML OK`.

- [ ] **Step 3: Sanity-check svu computes a sane version locally (best-effort)**

Run (skip if `go install` can't reach the network — this only validates svu's behavior, not the workflow):
```bash
export PATH="$(go env GOPATH)/bin:$PATH"
command -v svu >/dev/null || go install github.com/caarlos0/svu/v2@latest
git fetch --tags >/dev/null 2>&1
echo "current latest tag: $(git describe --tags --abbrev=0 2>/dev/null)"
echo "svu next: $(svu next 2>&1)"
```
Expected: `svu next` prints a `vX.Y.Z` >= the current latest tag (e.g. `v1.14.0` or a bump from it). If svu isn't installable here, note it and rely on the live `dry_run` check post-merge.

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/tag.yml
git commit -m "ci: add svu-based Tag Release workflow (manual dispatch)"
```

---

## Task 4: PR-title lint workflow

**Files:**
- Create: `.github/workflows/pr-title.yml`

- [ ] **Step 1: Create `.github/workflows/pr-title.yml`**

```yaml
name: PR Title

on:
  pull_request_target:
    types: [opened, edited, synchronize, reopened]

permissions:
  pull-requests: read

jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: amannn/action-semantic-pull-request@v5
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        with:
          types: |
            feat
            fix
            docs
            chore
            ci
            build
            test
            refactor
            perf
            style
            revert
```

- [ ] **Step 2: Verify YAML validity**

Run:
```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/pr-title.yml')); print('YAML OK')"
```
Expected: `YAML OK`.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/pr-title.yml
git commit -m "ci: enforce Conventional Commit PR titles"
```

---

## Task 5: Delete NEXT_RELEASE.md and its gitignore entry

**Files:**
- Delete: `NEXT_RELEASE.md`
- Modify: `.gitignore`

- [ ] **Step 1: Delete the file and the gitignore line**

Run:
```bash
git rm NEXT_RELEASE.md
python3 - <<'PY'
p='.gitignore'
lines=open(p).read().splitlines(keepends=True)
out=[l for l in lines if l.rstrip('\n') != 'release-notes.md']
assert len(out)==len(lines)-1, "expected to remove exactly one line"
open(p,'w').writelines(out)
print("removed release-notes.md from .gitignore")
PY
```
Expected: `git rm` removes the file; `removed release-notes.md from .gitignore`.

- [ ] **Step 2: Verify it's gone and `release-assets/` survives**

Run:
```bash
test ! -e NEXT_RELEASE.md && echo "NEXT_RELEASE.md gone"
grep -nE 'release-notes\.md' .gitignore || echo "release-notes.md ignore removed"
grep -nE 'release-assets/' .gitignore
```
Expected: `NEXT_RELEASE.md gone`; `release-notes.md ignore removed`; and `release-assets/` still listed in `.gitignore`.

- [ ] **Step 3: Confirm no remaining NEXT_RELEASE.md references in active config**

Run:
```bash
grep -rnE 'NEXT_RELEASE' .github/ .goreleaser.yaml README.md docs/deployment.md 2>/dev/null || echo "no active references"
```
Expected: `no active references`. (Historical mentions inside `task_plan.md` or `docs/superpowers/` are intentionally left untouched — they are point-in-time records.)

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "chore: remove NEXT_RELEASE.md (changelog now generated by GoReleaser)"
```

---

## Final verification

- [ ] **Step 1: All workflow YAML valid + goreleaser config valid**

Run:
```bash
export PATH="$(go env GOPATH)/bin:$PATH"
for f in release tag pr-title; do
  python3 -c "import yaml; yaml.safe_load(open('.github/workflows/$f.yml')); print('YAML OK: $f.yml')"
done
goreleaser check
```
Expected: `YAML OK` for all three workflows; goreleaser config validated.

- [ ] **Step 2: Push the branch and open a PR**

```bash
git push -u origin ci/automated-release-notes
gh pr create --repo nexspence/nexspence-core --base main \
  --title "ci: automated release notes (GoReleaser changelog + svu + PR-title lint)" \
  --body "Implements docs/superpowers/specs/2026-06-09-automated-release-notes-design.md. Replaces the manual NEXT_RELEASE.md flow (which produced an empty v1.14.0 body) with GoReleaser-native changelog + footer, an svu-based manual Tag workflow, and a Conventional-Commit PR-title lint."
```
(Per repo policy: branch + PR, never push to `main` directly.)

- [ ] **Step 3: Live verification (after merge — document, do not block the PR on it)**

Note in the PR that the end-to-end check is: run the **Tag Release** workflow with `dry_run: true` (confirm it prints a sane `vX.Y.Z`), then run it without `dry_run` to cut the next release, and confirm the resulting GitHub release body is **non-empty** with grouped sections + the Docker footer.

---

## Notes for the implementer

- **No application/Go source changes.** This is entirely CI/release config. If you find yourself editing `cmd/` or `internal/`, stop — it's out of scope.
- **`use: git`, not `github-native`.** The build repo (`nexspence-core`) and release repo (`nexspence/nexspence`) differ; `github-native` would read the wrong repo's PRs. Don't change it.
- **Tag push must use `RELEASE_PAT`.** Tags pushed with the default `GITHUB_TOKEN` do not trigger other workflows, so `tag.yml` would not start `release.yml`. The checkout `token:` is what matters.
- **PR-title check is advisory on the free plan.** The private repo can't enforce required checks via branch protection (free plan), so the lint is a visible signal, not a hard gate. That's expected per the spec.
- **After rollout, update memory** `feedback_next_release` — the "always append to NEXT_RELEASE.md" rule is now obsolete (file deleted; changelog auto-generated). (Memory edit, not a repo change.)
