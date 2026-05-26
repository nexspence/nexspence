### 🐛 Bug Fixes

- **HA startup fix** — `docker-compose.yml` and `docker-compose.ha.yml` now default to `./config.yaml` instead of `./config.yaml.example`; the previous default caused a `read /app/config.yaml: is a directory` crash when `config.yaml.example` was extracted as an empty directory from the release zip

### ✨ Features


### 🐛 Bug Fixes

_No bug fixes in this release._
