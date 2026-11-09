#!/usr/bin/env python3
"""Run govulncheck and fail on every reported vulnerability, except the ones
listed in scripts/vulnerability-allowlist.txt with a written reason.

Why the wrapper exists: the Go vulnerability database still lists every
version of github.com/crewjam/saml as affected by GO-2026-6909, although the
two defects it describes are fixed upstream from v0.6.0 on:

  * `Subject.NameID` had no XML namespace in its tag, so a `<NameID>` from a
    foreign namespace inside a validly signed assertion became the
    authenticated subject;
  * `validateAssertion` dereferenced the assertion's optional `<Subject>` and
    `<Conditions>` without a nil check, so a signed assertion without them
    panicked instead of being refused.

We depend on v0.6.3, where both are fixed (internal/auth/saml_dependency_test.go
pins the floor and checks the parsing behaviour). As long as the database
records no fixed version, govulncheck fails the gate for a dependency that is
not vulnerable — and a red gate on every pull request trains everyone to
ignore it.

The wrapper keeps the gate meaningful: it fails on any reported
vulnerability whose ID is not in the allowlist, and it propagates govulncheck's
own exit code and output when the allowlist does not cover the finding. The
allowlist is a plain text file, so every exception shows up in the diff.
"""

import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
ALLOWLIST = ROOT / "scripts" / "vulnerability-allowlist.txt"

# govulncheck's text output names every reachable vulnerability like this:
#   Vulnerability #1: GO-2026-6909
FINDING = re.compile(r"^Vulnerability #\d+: (GO-\d{4}-\d+)", re.MULTILINE)


def allowed_ids() -> dict[str, str]:
    allowed: dict[str, str] = {}
    if not ALLOWLIST.exists():
        return allowed
    for line in ALLOWLIST.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        parts = line.split(None, 1)
        allowed[parts[0]] = parts[1] if len(parts) > 1 else ""
    return allowed


def main() -> int:
    proc = subprocess.run(
        ["govulncheck", "./..."],
        capture_output=True, text=True, cwd=ROOT,
    )
    output = proc.stdout + proc.stderr
    print(output, end="")

    if proc.returncode == 0:
        return 0

    found = sorted(set(FINDING.findall(output)))
    allowed = allowed_ids()
    uncovered = [v for v in found if v not in allowed]

    if not found or uncovered:
        # Either govulncheck failed for a reason of its own (a parse error, a
        # broken build), or it reported something the allowlist does not
        # cover. Both are failures.
        if uncovered:
            print(f"\nnot allowlisted: {', '.join(uncovered)}", file=sys.stderr)
        return proc.returncode

    for vuln in found:
        print(f"\nallowlisted: {vuln} — {allowed.get(vuln, '')}", file=sys.stderr)
    print(
        "\nEvery reported vulnerability is on the allowlist. The allowlist "
        "entry states why it does not apply to the dependency in use.",
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
