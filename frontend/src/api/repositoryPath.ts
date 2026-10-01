// The same-origin URL an asset is served from (and uploaded to): the shape the
// server computes for downloadUrl, minus the configured base URL so the request
// carries the session. The repository name is one path segment and is encoded
// whole — unencoded, "demo#2" would reach repository "demo" (#570). In the
// asset path only `?` and `#` are escaped: they would end the path, while
// everything else is stored exactly as the format's handler expects it.
export function repositoryPath(repo: string, path: string): string {
  const clean = path.replace(/^\/+/, '').replace(/[?#]/g, (ch) => encodeURIComponent(ch))
  return `/repository/${encodeURIComponent(repo)}/${clean}`
}
