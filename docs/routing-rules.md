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

A group that combines a public proxy with a hosted repository asks its members
in `member_names` order. Without a rule, a request for an internal artifact
that the proxy has not cached is forwarded to the public upstream, and the
name is disclosed even when the upstream answers `404`.

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

Member order and index merging are otherwise unchanged. A group still asks its
members in the configured order and merges index documents from all of them.
