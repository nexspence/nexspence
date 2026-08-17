# Contributing to Nexspence

Thanks for wanting to help. This page covers the two things that are easy to get
wrong — the licence your contribution carries, and how to get a change reviewed.

## Licensing of contributions

Nexspence is licensed under **AGPL-3.0-or-later** (SPDX). Contributions are
accepted under the same licence: by submitting a pull request you agree that
your work is licensed under AGPL-3.0-or-later and that you have the right to
license it that way.

There is no CLA. You keep the copyright in what you write — the project claims
none of it. The practical consequence, stated plainly rather than buried: because
copyright stays spread across everyone who has contributed, the project cannot
be relicensed later without asking each of you. That is deliberate.

There is no sign-off requirement either: opening the pull request is the
statement, and nothing checks for a `Signed-off-by` line.

## Making a change

- **Branch from `main`.** Pull requests target `main`.
- **Conventional commit titles.** The PR title is checked by CI and drives
  release versioning, so it is not decoration: `feat:` and `fix:` produce a
  release, other types do not. Allowed: `feat`, `fix`, `docs`, `chore`, `ci`,
  `build`, `test`, `refactor`, `perf`, `style`, `revert`.
- **Tests come with the change.** Backend `go test ./...`, frontend
  `npm test`. Integration tests need Docker: `make test-integration`.
- **Run the checks before pushing** — `make lint`, `make test`, and in
  `frontend/`: `npx tsc --noEmit`, `npx eslint src`. CI runs them anyway; running
  them locally is faster than a round trip.
- **Coverage gates are real.** CI enforces a per-package minimum on both the Go
  and the frontend suites.

## Reporting bugs and asking questions

- Bugs and feature requests: [GitHub issues](https://github.com/nexspence/nexspence/issues).
- Security problems: please do **not** open a public issue. Contact the
  maintainer directly (below) so a fix can ship before the details are public.

## Maintainer

Telegram: [@skensel](https://t.me/skensel)
