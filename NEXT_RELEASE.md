### 🛠️ Tooling

* **`nxs` CLI v0.1.0** — official command-line client for Nexspence, available at [github.com/skensell201/nxs](https://github.com/skensell201/nxs). Supports all repository operations (`repo list/create/delete/info`), artifact upload/download with progress bars (`push`, `pull`), component search, user and role management, cleanup policy execution, live Nexus migration, and server health monitoring. Multi-context config (`~/.config/nxs/config.yaml`) with env var overrides (`NXS_URL`, `NXS_TOKEN`). Rich output by default; `--json` and `--plain` modes for scripting and CI/CD. Binaries for Linux/macOS/Windows (amd64 + arm64) via GitHub Releases; one-line install: `curl -sSfL https://raw.githubusercontent.com/skensell201/nxs/main/install.sh | sh`.

### 📚 Documentation

* **Deployment guide rewritten** — removed "From Source" section (not applicable), replaced all `git clone` instructions with downloads from [github.com/skensell201/nexspence/releases](https://github.com/skensell201/nexspence/releases), fixed `ghcr.io` image path to `ghcr.io/skensell201/nexspence`.
* **Helm README** — added link to release zip, clarified chart path (`deploy/helm/nexspence/` inside the extracted archive).
* **HA setup guide** — replaced `git clone` with release download link.
* **README** — added CLI Tool section with install one-liner and link to `nxs`; fixed all repo URLs to `skensell201/nexspence`; updated roadmap to mark `nxs` CLI as complete.

### 🐛 Bug Fixes

_No bug fixes in this release._
