package terraform

// Root-relative client-facing links (#821).
//
// Every URL this format hands to a client is built from repoPath — never from
// http.base_url, and never from the request's Host or X-Forwarded-Host header.
// A root-relative path carries no host, so the client resolves it against the
// host it already reached: behind a reverse proxy that is the only host the
// client can reach.
//
// The terraform client resolves every one of these against the request it is
// answering, which is why a relative reference is enough:
//
//   - the service discovery document: terraform-svchost resolves a relative
//     "providers.v1" / "modules.v1" against the discovery URL it fetched
//     (disco/host.go: parseURL -> discoURL.ResolveReference);
//   - the provider download_url: the registry client resolves it against the
//     URL of the download request (getproviders/registry_client.go:
//     downloadURL = resp.Request.URL.ResolveReference(downloadURL));
//   - the module X-Terraform-Get header: the registry client treats a value
//     starting with "/" as a relative URL and resolves it against the
//     download request (internal/registry/client.go: download.ResolveReference).
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
