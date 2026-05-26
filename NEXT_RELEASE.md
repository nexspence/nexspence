### ✨ Features


### 🐛 Bug Fixes

- **Docker bind-mount fix** — added `RUN touch /app/config.yaml` to Dockerfile; without a placeholder file in the image, Docker Desktop (Mac) created a directory at that path and failed to mount the host config file with `not a directory` error
