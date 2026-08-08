# Migration from Nexus

Nexspence imports a live **Sonatype Nexus Repository 3 (OSS / Community)** instance over its REST API: repository definitions, the artifacts inside them, container images, and the security model that governs access to all of it. The source instance keeps serving traffic throughout — nothing is written back to it.

A migration is a **job**. You create one, it runs in the background, and you watch it from **Admin → Migration**. It can be paused, resumed, and it survives a restart of Nexspence.

## What gets migrated

| Item | Detail |
|------|--------|
| Repositories | Hosted, proxy and group definitions — format, type, group membership, proxy remote URL |
| Artifacts | Every component in each **hosted** repository, streamed through the same storage path an upload takes |
| Container images | Manifests, image indexes and the blobs they reference, re-created so the image is pullable byte-for-byte |
| Privileges | Everything except Nexus built-ins, which this instance ships its own copies of |
| Roles | Including nested roles, flattened (see below) |
| Users | Local accounts with a temporary password; LDAP/OIDC/SAML accounts as external references |
| Routing rules | `ALLOW` / `BLOCK` rules with their regex matchers |

**Not migrated:** cleanup policies and task schedules — Nexus OSS does not expose either through its REST API. Proxy repository *caches* are not copied either: a proxy re-fetches from its own upstream, so copying its cache would only duplicate bytes that Nexspence would fetch anyway.

## Running a migration

### 1. Test the connection

In **Admin → Migration → New Migration**, enter the Nexus URL and admin credentials and press **Test connection**. This is a pure read — no job is created, so it is safe to press as often as you edit the form. On success it lists the repositories it can see; on failure it shows the reason the source gave.

The equivalent API call:

```bash
curl -X POST https://nexspence.example.com/api/v1/migration/preview \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"sourceUrl":"https://nexus.example.com","username":"admin","password":"…"}'
```

`200` with `{"reachable":true,"repoCount":N,"repos":[…]}`, `422` if the URL is missing or is not an absolute `http(s)` URL, `502` if the source could not be reached — with the underlying error, not a generic failure.

### 2. Choose the scope

Every stage is an independent flag. A repositories-only run touches nothing else; a security-only run creates no repositories.

```bash
curl -X POST https://nexspence.example.com/api/v1/migration/jobs \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{
    "sourceUrl": "https://nexus.example.com",
    "credentials": {"username": "admin", "password": "…"},
    "scope": {
      "migrateRepos": true,
      "migrateBlobs": true,
      "migratePrivileges": true,
      "migrateRoles": true,
      "migrateUsers": true,
      "migrateRoutingRules": true
    }
  }'
```

Every flag defaults to `true`. `migratePolicies` is the older, coarser switch kept for API compatibility: when a request does not name the three security scopes, they fall back to it.

### 3. Watch it

The job card shows repositories done, assets done, and an error count. `GET /api/v1/migration/jobs/{id}` returns the same numbers.

## The order things happen in

The sequence is fixed, because each stage depends on the one before it.

1. **Repositories** — hosted, then proxy, then group, so a group's members already exist when the group naming them is created. A member that was not migrated is left out of the group rather than blocking it.
2. **Artifacts** — hosted repositories only.
3. **Privileges** → **Roles** → **Users** — a role references privileges by name and a user references roles by name, so each must exist before the thing that names it.
4. **Routing rules** — independent of everything above.

## Container images

Nexus stores registry blobs content-addressed, lists them under a placeholder image name (`/v2/-/blobs/<digest>`), and its component listing reports only the manifest. Copying what that listing reports would produce an image nobody can pull.

So for a `docker` or `oci` repository the migration reads each manifest and uses it as the index of what to fetch:

- an **image index** (multi-platform) is followed into each child manifest first;
- the manifest's config and layer blobs are fetched from `/v2/<image>/blobs/<digest>` — the path the image actually references them under — and stored **before** the manifest, so the registry never serves a manifest whose layers are still missing;
- a **tagged** manifest also gets its content-digest alias registered, because a client that pulls by tag immediately re-fetches the same manifest by digest.

Shared layers are transferred once: a blob already present for an image is skipped.

## Nested roles

Nexspence has no nested-role table, so a Nexus role that nests another is **flattened**: the parent takes the union of its own privileges and everything it inherits. The role graph is topologically sorted first, so one pass is enough.

A cycle in the role graph is reported in `errorCount` and `lastError`. The roles involved are still created — each keeping only its own privileges — because dropping a role silently is worse than migrating a flatter version of it.

## Migrated users and passwords

A Nexus password hash cannot come across. A **local** account is therefore created with a random temporary password and `mustResetPassword`, and an **externally-authenticated** account (LDAP, OIDC, SAML) is created with no local credential at all — its identity provider still owns it.

Migrated users log in normally; the flag drives the prompt to choose a new password, and clears the moment one is set. Blocking login until reset was considered and rejected: it strands every migrated user at once, which is the opposite of what an admin bootstrapping a hundred accounts needs.

## Re-running, pausing, restarting

Everything the migration creates is matched by its logical name — repository name, privilege name, role name, username, rule name, asset path. Anything already present is **skipped, not overwritten**. A repository an operator has since reconfigured is left exactly as it is.

That single property is what makes the three lifecycle operations cheap:

- **Re-running** a finished job costs only whatever was not there the first time.
- **Pause** (`POST /api/v1/migration/jobs/{id}/pause`) stops the run where it stands and keeps its progress. **Resume** starts a fresh pass that skips what is done.
- **A restart** of Nexspence re-attaches to every job that was still running, on startup. The source credential is persisted for exactly this reason — sealed with the instance encryption key, the same way replication target passwords are, and never returned by the API.

## Failures

A migration copies thousands of independent things, so one that will not come across is counted in `errorCount`, named in `lastError`, and the run continues. Only a failure that makes the rest impossible — the source becoming unreachable, credentials being rejected — ends the job as `error`.

Common entries:

| `lastError` | Meaning |
|-------------|---------|
| `format "rubygems" has no Nexspence equivalent` | The source has a repository of a format Nexspence does not serve. It is skipped; everything else still migrates. |
| `member "x" was not migrated and is left out` | A group named a member that could not be created (usually an unsupported format). |
| `matcher "…" does not compile` | A routing rule is stored whole or not at all — a half-applied `ALLOW` rule would quietly change what the source permitted. |

## Requirements and limits

- **Nexus admin credentials.** The security endpoints (`/service/rest/v1/security/*`) are admin-only.
- **Network reachability.** Migration targets are user-supplied URLs, so they go through the same SSRF guard as webhooks and proxy upstreams. A Nexus on an internal address needs that range in `outbound.allowed_internal_cidrs`.
- **Full repository configuration** comes from `/service/rest/v1/repositorySettings`. An instance that does not expose it — an older version, or credentials without admin rights — falls back to the basic repository listing, which still names every repository but cannot describe group membership or proxy remotes.
- Regex matchers are copied verbatim. Nexus and Nexspence both use Go-compatible syntax; an implementation on a different engine should re-check them.

## API reference

Full request and response shapes are in [`docs/api-spec.yaml`](api-spec.yaml) under the **Migration** tag.

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/api/v1/migration/preview` | Test the connection; creates nothing |
| `GET` | `/api/v1/migration/jobs` | List jobs |
| `POST` | `/api/v1/migration/jobs` | Create a job and start it |
| `GET` | `/api/v1/migration/jobs/{id}` | Job status and progress |
| `POST` | `/api/v1/migration/jobs/{id}/pause` | Stop the run, keep the progress |
| `POST` | `/api/v1/migration/jobs/{id}/resume` | Start a fresh pass |
| `DELETE` | `/api/v1/migration/jobs/{id}` | Stop the run and delete the record |
