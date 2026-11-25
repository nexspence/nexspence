package conda

// Root-relative client-facing links (#821).
//
// Every URL this format hands to a client is built from repoPath — never from
// http.base_url, and never from the request's Host or X-Forwarded-Host header.
// A root-relative path carries no host, so the client resolves it against the
// host it already reached: behind a reverse proxy that is the only host the
// client can reach.
//
// The conda client resolves a package url against the channel it read
// repodata.json from, so a root-relative path (rather than a path-relative
// one, which would resolve against the directory of repodata.json and
// duplicate the subdir) names the package on the very host the client used.
//
// http.base_url is the server's own configuration, not the client's view of
// it: a client that reached the server through nginx at https://myrepo.ru was
// never at http://localhost:8081, so a link minted from the configured base
// URL points somewhere unreachable.
//
// A host derived from the request would fix that but is forbidden here: it is
// a cache-poisoning vector (one request with a forged Host header would
// poison a shared cache entry for every other client), and http.base_url
// together with http.trusted_proxies exists precisely to control that trust.

// repoPath returns the root-relative path of a repository — the prefix every
// client-facing URL of this format is built from.
func repoPath(repoName string) string {
	return "/repository/" + repoName
}
