# Routing Rules

A routing rule is a named list of regular expressions (`matchers`) with a mode:

- `ALLOW`: a path passes only if it matches at least one matcher.
- `BLOCK`: a path passes only if it matches none.

Matchers are tested against the repository-relative request path, for example
`/com/acme/lib/1.0/lib-1.0.jar` (Maven), `/simple/acme-tool/` (PyPI) or
`/v2/acme/app/manifests/1.0` (Docker / OCI). A rule only applies to reads
(`GET` and `HEAD`).

Rules are managed under **Admin → Routing Rules** or through
`/service/rest/v1/routing-rules`, and attached to a repository with its
`routingRuleId`.

## Where a rule applies

| Attached to | Effect of a refused path |
|---|---|
| **Group** | The group answers `404` without asking any member. |
| **Proxy** | The proxy answers `404`. Neither its cache nor `remote_url` is consulted, so the name never reaches the upstream, including a Docker Hub token `scope`. Inside a group the refusal is an ordinary miss: the group moves on to the next member. |
| **Hosted** | Nothing. Hosted repositories ignore routing rules, as in Nexus. |

## Keeping a private namespace off a public upstream

A group asks its hosted members before its proxy members, so an artifact the
hosted repository already has is never requested upstream, and a per-package
index (`maven-metadata.xml`, an npm packument, a PyPI project page, Go
`@v/list`, a NuGet version list, Terraform `versions`) that a hosted member
has is merged from the hosted members alone. A name that is not published
yet, such as a version that is still being built or a typo, still falls
through to the proxy, and the upstream sees it even when it answers `404`.

Attach a `BLOCK` rule to the **proxy**, not the group:

```json
{ "name": "acme-private", "mode": "BLOCK", "matchers": ["^/com/acme/"] }
```

With the group `maven-public = [maven-central, maven-internal]`:

- `/com/acme/...` is refused by `maven-central`, so Maven Central never sees
  it, and `maven-internal` serves it.
- Every other path is fetched through `maven-central` as before.
- Index documents such as `com/acme/lib/maven-metadata.xml` are merged from
  `maven-internal` alone.

The same rule on the group would block `/com/acme/...` for every member,
including `maven-internal`.

## Group member order

Within a group, hosted members are asked first and proxy members after them;
each kind keeps its order from `member_names`. A file request returns the
first member that has the path. An index request merges every member's
answer, except that a per-package index a hosted member answers with content
is not asked of the proxy members: public versions of a locally published
name are not mixed into it. Repository-wide indexes (apt `Release`, Helm
`index.yaml`, the Docker `_catalog`, …) are always merged from every member.
