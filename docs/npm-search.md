# npm and Unity package discovery

Nexspence serves `GET /repository/<npm-repository>/-/v1/search` on hosted,
proxy and group npm repositories. npm clients can use the same registry URL
for publishing, installing and searching:

```sh
npm search --registry https://registry.example.com/repository/npm-group/ com.studios
```

Unity accepts this search endpoint for scoped registry package discovery.
Configure the registry URL and your package name prefixes in the project's
scoped registry settings. See Unity's [scoped registry hosting guide](https://docs.unity3d.com/6000.0/Documentation/Manual/upm-scoped-host.html).

The response follows npm's [registry search API](https://github.com/npm/registry/blob/main/docs/REGISTRY-API.md#get-v1search):
`objects[].package`, `objects[].score`, `objects[].searchScore`, `total` and
`time`. `HEAD` is supported; publishing to the search endpoint is not.

- **Hosted:** search package names, descriptions and keywords, matching text
  tokens without case sensitivity. An empty text lists available packages.
  Each name appears once, using its `latest` dist-tag or its highest version
  if no latest tag exists. Results are ordered by name. Local quality,
  popularity and maintenance scores are neutral (zero).
- **Proxy:** forward the query, including upstream search qualifiers and
  ranking weights. Search answers are computed for the request and are never
  stored as cached artifacts.
- **Group:** collect the complete matching member lists before deduplicating
  and paging. Hosted members take precedence over proxy members; configured
  member order decides between members of the same type. Results are ordered
  by name, and `total` counts unique visible packages. A failed member search
  fails the request instead of reporting an incomplete catalog as complete.

`size` defaults to 20 and is capped at 250; `from` defaults to zero. Both must
be non-negative integers. Pagination happens after deduplication and access
filtering. A user with a path-scoped read grant can search the repository, but
only package names that pass that grant appear in the answer or its total.
Anonymous access follows the instance and repository settings, and API token
scopes still apply.

For a group or a path-restricted proxy search, Nexspence may fetch several
upstream pages to compute the visible result set. A single upstream search
response and the accumulated upstream pages are each limited to 32 MiB;
collection is also limited to 10,000 upstream results. Exceeding either bound
returns an error asking for a narrower text query. These computed search
limits are separate from the configurable buffer used to rewrite cached
package metadata. Hosted search scans the repository's component metadata;
use a specific text query for large catalogs.

The legacy `/-/all` endpoint is not implemented. Unity can use `/-/v1/search`.
