# nxs CLI — Design Spec

**Date:** 2026-05-17  
**Status:** approved  
**Scope:** standalone CLI client for Nexspence artifact repository manager

---

## Overview

`nxs` is a command-line interface for Nexspence, distributed as a standalone binary. It lives in a separate repository (`github.com/nexspence/nxs`) with its own release cycle. It communicates exclusively via the Nexspence REST API — no Go internal package imports from nexspence-core.

---

## Repository Structure

```
nexspence/nxs/
├── cmd/nxs/main.go
├── cmd/
│   ├── root.go          # root cobra cmd, global flags, Printer injection
│   ├── login.go         # nxs login / nxs logout
│   ├── context.go       # nxs context {list,use}
│   ├── repo.go          # nxs repo {list,create,delete,info}
│   ├── push.go          # nxs push <repo> <path> <file>
│   ├── pull.go          # nxs pull <repo> <path> [--output]
│   ├── search.go        # nxs search
│   ├── user.go          # nxs user {list,create}
│   ├── role.go          # nxs role assign
│   ├── cleanup.go       # nxs cleanup run
│   ├── migrate.go       # nxs migrate from
│   └── health.go        # nxs health [--watch]
├── internal/
│   ├── client/
│   │   ├── client.go    # resty wrapper, auth middleware, error handler
│   │   ├── repos.go
│   │   ├── components.go
│   │   ├── users.go
│   │   ├── cleanup.go
│   │   └── migration.go
│   ├── config/
│   │   └── config.go    # load config.yaml + env override
│   └── output/
│       ├── printer.go   # Printer interface
│       ├── rich.go      # lipgloss tables + colors (default)
│       ├── plain.go     # TSV, no colors
│       ├── json.go      # json.MarshalIndent
│       └── progress.go  # progressbar wrapper (writes to stderr)
├── .github/workflows/
│   ├── ci.yml
│   └── release.yml
├── .goreleaser.yaml
└── go.mod               # module github.com/nexspence/nxs
```

---

## Commands

### Auth
```bash
nxs login --url http://nexspence:8080 --user admin   # prompts password, saves token
nxs logout
nxs context list
nxs context use <name>
```

### Repositories
```bash
nxs repo list [--format maven] [--type hosted]
nxs repo create <name> --format maven --type hosted [--blob-store default]
nxs repo delete <name> [--force]
nxs repo info <name>
```

### Artifacts
```bash
nxs push <repo> <remote-path> <local-file>   # user provides full remote path
nxs pull <repo> <remote-path> [--output ./]
nxs search [--repo <name>] [--format maven] [--q keyword] [--tag k=v]
```

### Users & Roles
```bash
nxs user list
nxs user create <username> --email x@x.com [--role nx-admin]
nxs role assign <username> <role-name>
```

### Operations
```bash
nxs cleanup run <policy-name>
nxs migrate from <nexus-url> --user admin [--repos] [--users] [--blobs]
nxs health [--watch]
```

### Global Flags
```
--json      machine-readable JSON output
--plain     plain TSV, no colors or borders
--url       override NXS_URL env / config
--token     override NXS_TOKEN env / config
--context   select named context explicitly
```

---

## Configuration

**Priority (highest to lowest):**
1. CLI flags (`--url`, `--token`)
2. Environment variables (`NXS_URL`, `NXS_TOKEN`, `NXS_CONTEXT`)
3. `~/.config/nxs/config.yaml`

**Config file format:**
```yaml
current_context: prod
contexts:
  prod:
    url: https://nexspence.company.com
    token: nxs_abc123
  local:
    url: http://localhost:8080
    token: nxs_xyz789
```

Config is loaded once in `PersistentPreRunE` of the root command. Commands exit early with a clear message if no URL is configured.

---

## HTTP Client

Built on `resty`. A single `Client` struct wraps all API calls:

```go
type Client struct {
    r    *resty.Client
    base string
}

func New(url, token string) *Client {
    r := resty.New().
        SetBaseURL(url).
        SetHeader("Authorization", "Bearer "+token).
        SetHeader("Accept", "application/json").
        OnAfterResponse(checkHTTPError)
    return &Client{r: r, base: url}
}
```

`checkHTTPError` is a single response middleware that maps HTTP status codes to human-readable error messages:
- `401` → `invalid token or session expired`
- `403` → `insufficient permissions`
- `404` → `<resource> does not exist`
- `5xx` → `server error: <body>`

**Login flow:**
1. Prompt URL if not provided
2. Prompt username + password (hidden input via `golang.org/x/term`)
3. `POST /api/v1/login` → JWT token
4. Save token to `~/.config/nxs/config.yaml` under context name
5. Print: `Logged in to <url> as <username>`

**Push/Pull:**
- `push`: streams file body to `PUT /repository/<repo>/<path>` with progress bar on stderr
- `pull`: streams response body to local file with progress bar on stderr
- Progress bar is suppressed in `--json` and `--plain` modes

---

## Output Layer

All presentation is isolated in `internal/output/`. Subcommands receive a `Printer` injected via cobra context — they never call `fmt.Printf` directly.

```go
type Printer interface {
    Table(headers []string, rows [][]string)
    Success(msg string)
    Error(msg string)
    JSON(v any)
}
```

| Mode | Printer | Format |
|------|---------|--------|
| default | `RichPrinter` | lipgloss tables, colored status dots |
| `--plain` | `PlainPrinter` | tab-separated, no ANSI |
| `--json` | `JSONPrinter` | `json.MarshalIndent` to stdout |

Progress bar (`schollz/progressbar`) writes to stderr so it doesn't corrupt piped JSON output.

---

## Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/spf13/cobra` | command parsing |
| `github.com/go-resty/resty/v2` | HTTP client |
| `github.com/charmbracelet/lipgloss` | table rendering + colors |
| `github.com/schollz/progressbar/v3` | upload/download progress |
| `golang.org/x/term` | hidden password input |

---

## Distribution

**goreleaser** triggers on `v*` git tags:
- Builds for: `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`
- Archives: `.tar.gz` (unix), `.zip` (windows)
- Creates GitHub Release with checksums
- Updates `nexspence/homebrew-tap` with new formula

**Installation:**
```bash
# Homebrew (macOS / Linux)
brew install nexspence/tap/nxs

# curl install script
curl -sSfL https://raw.githubusercontent.com/nexspence/nxs/main/install.sh | sh
```

**CI (every PR):** `go test ./...`, `go vet ./...`, `go build ./...`

---

## Out of Scope (v1)

- Interactive TUI (bubbletea navigation)
- Shell completion (`nxs completion bash`)  
- Terraform provider
- Plugin system
- Windows installer (.msi)
