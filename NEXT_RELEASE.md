### ✨ Features


### 🐛 Bug Fixes

- **Docker bind-mount fix** — `config.yaml.example` is now bundled in the image as `/app/config.yaml`; without a file at that path Docker Desktop (Mac) created a directory and failed with `not a directory` on bind-mount
