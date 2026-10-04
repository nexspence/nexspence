#!/usr/bin/env python3
"""The chart deploys nexspence as a Deployment or a StatefulSet (#563).

Renders the chart for each workload kind and storage layout and checks the
objects that differ between them: the controller kind, the blob volume (a
standalone PVC for a Deployment, a volumeClaimTemplate for a StatefulSet on
chart-managed local storage), the headless Service a StatefulSet needs, the
HPA target, and the refusals for layouts that would split the repository
across per-pod volumes. Stdlib only, so it runs wherever helm does.
"""

import re
import subprocess
import sys
from pathlib import Path

CHART = Path(__file__).resolve().parent.parent / "deploy" / "helm" / "nexspence"
FAILURES = []


def render(*sets):
    args = ["helm", "template", "t", str(CHART)]
    for s in sets:
        args += ["--set", s]
    return subprocess.run(args, capture_output=True, text=True)


def docs(out):
    return [d for d in re.split(r"^---\s*$", out, flags=re.M) if re.search(r"^kind:", d, re.M)]


def kind_of(doc):
    return re.search(r"^kind:\s*(\S+)", doc, re.M).group(1)


def of_kind(out, kind):
    # Only the chart's own objects: the bundled PostgreSQL brings a
    # StatefulSet and Services of its own.
    return [d for d in docs(out) if kind_of(d) == kind
            and re.search(r"^  name: t-nexspence(-[a-z-]+)?$", d, re.M)]


def check(cond, msg):
    if not cond:
        FAILURES.append(msg)


def ok(*sets):
    r = render(*sets)
    check(r.returncode == 0, f"{sets}: render failed: {r.stderr.strip()}")
    return r.stdout


def refused(sets, needle):
    r = render(*sets)
    check(r.returncode != 0 and needle in r.stderr, f"{sets}: expected a refusal mentioning {needle!r}, got rc={r.returncode} {r.stderr.strip()}")


# Default: a Deployment with the chart's own PVC, exactly as before.
out = ok()
check(len(of_kind(out, "Deployment")) == 1, "default: one Deployment")
check(not of_kind(out, "StatefulSet"), "default: no StatefulSet")
check(len(of_kind(out, "PersistentVolumeClaim")) == 1, "default: the blobs PVC")
check(len(of_kind(out, "Service")) == 1, "default: one Service, no headless one")
check("claimName: t-nexspence-blobs" in out, "default: mounts the chart PVC")
# Switching to a StatefulSet (or to an existingClaim) drops the chart PVC from
# the manifest; without this helm would delete it, and every blob with it.
pvc = of_kind(out, "PersistentVolumeClaim")
check(pvc and "helm.sh/resource-policy: keep" in pvc[0], "default: the blobs PVC survives leaving the manifest")

# StatefulSet on chart-managed local storage: a volumeClaimTemplate per pod.
out = ok("workloadKind=StatefulSet")
sts = of_kind(out, "StatefulSet")
check(len(sts) == 1 and not of_kind(out, "Deployment"), "sts: one StatefulSet, no Deployment")
sts = sts[0] if sts else ""
check("serviceName: t-nexspence-headless" in sts, "sts: serviceName points at the headless Service")
check("volumeClaimTemplates:" in sts and re.search(r"volumeClaimTemplates:\s*\n\s*- metadata:\s*\n\s*name: blobs", sts), "sts: blobs volumeClaimTemplate")
check("storage: 100Gi" in sts and "ReadWriteOnce" in sts, "sts: template carries size and access mode")
check("claimName:" not in sts, "sts: no standalone claim for blobs")
check("updateStrategy:" in sts and "\n  strategy:" not in sts, "sts: updateStrategy, not strategy")
check(not of_kind(out, "PersistentVolumeClaim"), "sts: no standalone PVC")
headless = [d for d in of_kind(out, "Service") if "clusterIP: None" in d]
check(len(headless) == 1 and "name: t-nexspence-headless" in headless[0], "sts: headless Service")
check(len(of_kind(out, "Service")) == 2, "sts: the regular Service stays")

# StatefulSet on a shared existing claim or object storage: no per-pod volume.
sts = "".join(of_kind(ok("workloadKind=StatefulSet", "storage.local.existingClaim=shared", "replicaCount=3"), "StatefulSet"))
check(sts and "volumeClaimTemplates:" not in sts and "claimName: shared" in sts, "sts+existingClaim: mounts the shared claim")
sts = "".join(of_kind(ok("workloadKind=StatefulSet", "storage.type=s3", "storage.s3.bucket=b", "replicaCount=3"), "StatefulSet"))
check(sts and "volumeClaimTemplates:" not in sts, "sts+s3: no volumeClaimTemplate")

# HPA targets whichever controller the chart renders.
out = ok("workloadKind=StatefulSet", "storage.type=s3", "storage.s3.bucket=b", "autoscaling.enabled=true")
hpa = of_kind(out, "HorizontalPodAutoscaler")
check(len(hpa) == 1 and re.search(r"kind: StatefulSet", hpa[0]), "sts: HPA targets the StatefulSet")
out = ok("autoscaling.enabled=true", "storage.local.accessMode=ReadWriteMany")
hpa = of_kind(out, "HorizontalPodAutoscaler")
check(len(hpa) == 1 and re.search(r"kind: Deployment", hpa[0]), "default: HPA targets the Deployment")

# Per-pod volumes on more than one replica would split the repository.
refused(["workloadKind=StatefulSet", "replicaCount=2"], "workloadKind=StatefulSet")
refused(["workloadKind=StatefulSet", "autoscaling.enabled=true"], "workloadKind=StatefulSet")
refused(["workloadKind=DaemonSet"], "workloadKind")

# A release named like a YAML 1.1 boolean (n, y, no, on…) must still render
# its instance label as a string, or the API server refuses every object.
r = subprocess.run(["helm", "template", "n", str(CHART)], capture_output=True, text=True)
check(r.returncode == 0 and 'app.kubernetes.io/instance: "n"' in r.stdout
      and "app.kubernetes.io/instance: n\n" not in r.stdout, "release 'n': instance label quoted")

if FAILURES:
    print("\n".join("FAIL: " + f for f in FAILURES))
    sys.exit(1)
print("helm workload checks passed")
