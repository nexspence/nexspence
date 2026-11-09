#!/usr/bin/env python3
"""The chart must expose the request rate limit and a generic extraEnv.

Two gaps made the limit unusable from a chart install (#721):

- `auth.rate_limit_enabled`, `auth.rate_limit_rps` and `auth.rate_limit_burst`
  had no value, so an operator who needed a different limit had to mount a
  complete /app/config.yaml through extraVolumes — which collides with the
  file the chart mounts for `config.docker.subdomainConnector.aliases`.
- There was no way to pass an environment variable the chart does not model.

The env names are checked against internal/config/config.go by
scripts/check-helm-env.py; this script checks that the values actually reach
the rendered ConfigMap and Deployment.
"""

import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CHART = ROOT / "deploy" / "helm" / "nexspence"


def render(*args: str) -> str:
    out = subprocess.run(
        ["helm", "template", "check", str(CHART), *args],
        capture_output=True, text=True,
    )
    if out.returncode != 0:
        print(out.stderr, file=sys.stderr)
        sys.exit(f"helm template failed with arguments {args}")
    return out.stdout


def expect(text: str, needle: str, what: str) -> None:
    if needle not in text:
        print(f"missing from the rendered chart: {needle} ({what})", file=sys.stderr)
        sys.exit(1)


def main() -> int:
    # The defaults have to match the application defaults
    # (internal/config/config.go): a chart that pins a different number would
    # silently change the behaviour of every install.
    default = render()
    expect(default, 'NEXSPENCE_AUTH_RATE_LIMIT_ENABLED: "true"', "rate limit enabled by default")
    expect(default, 'NEXSPENCE_AUTH_RATE_LIMIT_RPS: "50"', "default rate limit rps")
    expect(default, 'NEXSPENCE_AUTH_RATE_LIMIT_BURST: "100"', "default rate limit burst")

    # An operator's own values have to reach the ConfigMap the Deployment
    # reads with envFrom.
    configured = render(
        "--set", "config.rateLimitEnabled=false",
        "--set", "config.rateLimitRps=7",
        "--set", "config.rateLimitBurst=9",
    )
    expect(configured, 'NEXSPENCE_AUTH_RATE_LIMIT_ENABLED: "false"', "rate limit disabled")
    expect(configured, 'NEXSPENCE_AUTH_RATE_LIMIT_RPS: "7"', "configured rps")
    expect(configured, 'NEXSPENCE_AUTH_RATE_LIMIT_BURST: "9"', "configured burst")

    # extraEnv reaches the container as a plain environment variable.
    extra = render(
        "--set", "extraEnv[0].name=NEXSPENCE_LOG_LEVEL",
        "--set", "extraEnv[0].value=debug",
    )
    expect(extra, "name: NEXSPENCE_LOG_LEVEL", "extraEnv name")
    expect(extra, "value: debug", "extraEnv value")

    print("chart exposes the rate limit and extraEnv")
    return 0


if __name__ == "__main__":
    sys.exit(main())
