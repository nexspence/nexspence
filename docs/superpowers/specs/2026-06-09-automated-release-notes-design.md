# Automated Release Notes: GoReleaser changelog + svu + PR-title lint

**Date:** 2026-06-09
**Status:** Design approved

## Goal

Make GitHub release notes generate automatically and reliably — no hand-maintained
`NEXT_RELEASE.md`, no fragile file handoff for the body. The version number is computed
from Conventional Commits, and PR-title correctness is enforced in CI.

## Context and root cause

Releases are cut by pushing an annotated tag `vX.Y.Z` to `nexspence-core`, which
triggers `.github/workflows/release.yml`. The build runs in `nexspence-core`, but the
GitHub release is created in the public repo **`nexspence/nexspence`** (cross-repo).

Previously the body was assembled like this: a hand-edited `NEXT_RELEASE.md` → a
"Build release notes" shell step concatenated it with a large hardcoded Docker/quickstart
block into `release-notes.md` → the file was passed to GoReleaser via
`--release-notes=release-notes.md`.

**Confirmed root cause:** on release **v1.14.0** the body came out empty (length 1)
vs. **3937** characters on the predecessor v1.13.0. v1.13.0 was cut by the old step
`gh release create --notes-file release-notes.md` (worked); v1.14.0 by the new
GoReleaser `--release-notes` step (body not populated). The switch to GoReleaser
`--release-notes` silently dropped the body; artifacts uploaded fine.

Conclusion: the `NEXT_RELEASE.md → release-notes.md → --release-notes` chain is manual
and fragile (easy to forget to update the file, boilerplate duplicated in YAML, can
fail silently). Replace it with native GoReleaser changelog generation.

> Note: the empty v1.14.0 body has already been fixed separately via `gh release edit`
> (body restored to 2195 chars). That is out of scope for this redesign.

## Decisions made during brainstorming

- **Strategy:** Option A — GoReleaser generates the changelog from git history using
  Conventional Commits. `NEXT_RELEASE.md` is deleted.
- **Versioning:** auto-computed with **svu** (`feat`→minor, `fix`→patch,
  `!`/`BREAKING`→major). Release trigger stays manual (`workflow_dispatch`): the human
  decides *when*, svu decides the *number*.
- **Title discipline:** add a PR-title lint (Conventional Commits). Because we
  squash-merge, the PR title becomes the commit subject becomes the changelog line.

## Non-goals

- Fully automatic release on every merge to `main` (releasing stays a manual trigger).
- Switching to release-please or git-cliff (considered, rejected — that was Option B).
- Changing the cross-repo publish scheme (build in `nexspence-core`, release in
  `nexspence/nexspence`) — stays as-is.
- Curated "marketing" release prose (if ever needed later — `release.header` in
  GoReleaser).

---

## 1. GoReleaser generates the release body

Changes in `.goreleaser.yaml`:

### 1.1 Remove the manual handoff
- Delete the `changelog: disable: true` block.
- In `release.yml`, "Run GoReleaser" step, remove the `--release-notes=release-notes.md`
  flag from `args` → leaving `release --clean`.

### 1.2 Add the native changelog

```yaml
changelog:
  use: git              # reads local nexspence-core git log; cross-repo safe
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

Important: `use: git` (**not** `github-native`). `github-native` would generate notes
from the history/PRs of the publish repo `nexspence/nexspence`, whose commits differ
(docs-sync) — which would be wrong. `use: git` parses local `nexspence-core` history
between the previous and current tag — which is what we want.

### 1.3 Move Docker/quickstart into the footer

The static block (image, platforms, run modes) that the shell step used to append moves
into `release.footer` as a templated YAML block:

```yaml
release:
  github:
    owner: nexspence
    name: nexspence
  mode: replace
  target_commitish: main
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

GoReleaser sets the release body itself (changelog between header/footer) via the same
API call that already uploads artifacts successfully. This removes the
"silently empty body" failure mode.

## 2. svu computes the version — new `tag.yml` workflow

New file `.github/workflows/tag.yml`, manually triggered:

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

Behavior: run it manually → svu computes the version from conventional commits since the
last tag → creates and pushes the tag → `release.yml` triggers on the tag push.
`dry_run: true` only shows the version (in the log and Job Summary); no tag is created.

Notes:
- The tag is pushed with `RELEASE_PAT` (not `GITHUB_TOKEN`), otherwise the tag push
  would not trigger `release.yml` (tags created by `GITHUB_TOKEN` don't trigger
  workflows).
- Manual `git tag -a vX.Y.Z && git push origin vX.Y.Z` remains a working fallback (for
  when a version must be set by hand).
- On the first run svu may compute relative to the current `v1.14.0`; non-conventional
  past PR titles land in the "Others" changelog group, which the lint then corrects
  going forward.

## 3. PR-title lint — new `pr-title.yml` workflow

New file `.github/workflows/pr-title.yml`:

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

Checks that the PR title follows Conventional Commits. A failing check blocks merge (via
a required check, if enabled in repo settings; on the free plan branch protection is
unavailable for the private repo — the check is still visible on the PR and acts as a
signal). Because we squash-merge, a correct PR title directly improves both the
changelog and svu's version math.

> Uses `pull_request_target` so the check works for fork PRs too (the title is read from
> PR metadata; PR code is not executed — the action only reads the title).

## 4. Remove the old manual machinery

- In `.github/workflows/release.yml` delete the **"Build release notes"** step (the
  whole ~40-line shell block with the Docker table).
- In `.github/workflows/release.yml` delete the **"Reset changelog for next release"**
  step (it reset `NEXT_RELEASE.md` and pushed to `main`).
- In the **"Run GoReleaser"** step, replace `args: release --clean
  --release-notes=release-notes.md` with `args: release --clean`.
- Delete the `NEXT_RELEASE.md` file.
- In `.gitignore` remove the `release-notes.md` line (the file is no longer generated).
  Keep the `release-assets/` line — that path is still needed for `extra_files` (the
  docker-compose zip).
- Find and remove references to `NEXT_RELEASE.md` elsewhere if any (README/CONTRIBUTING/
  docs) — replace with a short description of the new process.

## 5. Acceptance criteria / verification

- `goreleaser check` passes on the updated `.goreleaser.yaml`.
- Local dry run: on a clean clone the `git log` between the two latest tags groups as
  expected; `goreleaser release --snapshot --clean` runs and `dist/` contains the
  artifacts (the changelog in snapshot can be checked in `dist/CHANGELOG.md` if enabled,
  or in the output).
- `svu next` (locally, with svu installed) on the current `main` yields the correct next
  version relative to `v1.14.0`.
- YAML of all three workflows (`release.yml`, `tag.yml`, `pr-title.yml`) is valid
  (`yaml.safe_load`).
- `release.yml` no longer contains the "Build release notes" and "Reset changelog" steps
  and no longer references `release-notes.md`; `NEXT_RELEASE.md` is absent from the repo.
- Live check (after merge): running the **Tag Release** workflow with `dry_run: true`
  shows the correct version; then without `dry_run` it creates the tag and triggers the
  release; the new release has a **non-empty** body containing the grouped changelog +
  footer.

## 6. Impact on memory/process

- Memory `feedback_next_release` ("always append to NEXT_RELEASE.md before committing")
  becomes obsolete — update after rollout: the changelog is now generated from
  conventional PR titles, and `NEXT_RELEASE.md` no longer exists.

## 7. Deferred / possible improvements (out of scope)

- Curated release intro via `release.header`.
- Fully automatic release on a schedule / on merge.
- Installing svu from a release binary (faster than `go install`) — an optimization, not
  required.
