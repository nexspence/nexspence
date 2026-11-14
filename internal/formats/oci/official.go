package oci

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// Docker Hub keeps its official images under the "library/" namespace. A
// registry client that pulls "nginx" from docker.io asks for
// "docker.io/library/nginx" — the client does that normalization, the registry
// does not, and a proxy forwards the path it was given verbatim. So a client
// that asks this proxy for "nginx" reaches Hub as "nginx", which Hub refuses
// with 401 insufficient_scope: it only serves the official image as
// "library/nginx" (#757).
//
// The fix is the one Docker itself applies: the short name is tried first, and
// the "library/" form is tried when the upstream does not know the short one.
// The upstream that knows the short name is the one that answers it, so the
// fallback never shadows an image the upstream really has.

// upstreamImage is the resolved upstream name for a client-visible image name.
type upstreamImage struct {
	// remote is the upstream URL the names belong to. It keys the learned
	// mapping: a repository whose remote_url changed no longer shares the
	// names learned for the old one.
	remote string
	// primary is the name to ask upstream for.
	primary string
	// fallback is the name to try when upstream does not know primary. Empty
	// when no fallback applies.
	fallback string
}

// resolveUpstreamImage resolves the image name to use upstream, and the
// fallback to try when the upstream does not know it.
//
// The fallback applies to a Docker Hub proxy asked for a single-segment name:
// the official images live under "library/", and only Docker Hub has that
// namespace. Other upstreams serve their own top-level names, and rewriting
// those would answer with a different image than the client asked for.
func resolveUpstreamImage(repo *domain.Repository, image string) upstreamImage {
	resolved := upstreamImage{primary: image}
	remote, ok := dockerHubRemote(repo)
	if !ok {
		return resolved
	}
	if strings.Contains(image, "/") {
		// A namespaced name is already the upstream's own naming.
		return resolved
	}
	library := "library/" + image
	if officialNames.knows(remote, image) {
		// The upstream has already answered for this name: the short form is
		// known to be served under "library/".
		resolved.remote = remote
		resolved.primary = library
		return resolved
	}
	resolved.remote = remote
	resolved.fallback = library
	return resolved
}

// learn records that the upstream served this short name under "library/", so
// the next request for it goes straight there.
func (u upstreamImage) learn(image string) {
	if u.remote == "" || u.fallback == "" {
		return
	}
	officialNames.learn(u.remote, image)
}

// fetchUpstreamForImage asks the upstream for a path under an image name.
//
// When the upstream does not know the name the client asked for — a Docker Hub
// official image lives under "library/" and Hub answers the short name with
// 401 (#757) — the name it does know is tried as well, and the mapping is
// remembered for the next request.
//
// The returned upstreamImage carries the name the upstream answered under: the
// fallback name once the fallback served the request, the original one
// otherwise.
func fetchUpstreamForImage(ctx context.Context, repo *domain.Repository, upImage upstreamImage,
	image, suffix, rawQuery string, hdr http.Header,
) (*http.Response, upstreamImage, error) {
	resp, err := repoproxy.FetchUpstreamOnce(ctx, repo, "/v2/"+upImage.primary+suffix, rawQuery, hdr)
	if err != nil {
		return nil, upImage, err
	}
	if upImage.fallback == "" || !repoproxy.UpstreamMissesName(resp.StatusCode) {
		return resp, upImage, nil
	}
	alt, ok := fetchFallbackForImage(ctx, repo, upImage, suffix, rawQuery, hdr)
	if !ok {
		// The fallback did not answer. The primary answer stands.
		return resp, upImage, nil
	}
	// The fallback served the request: the primary answer is dropped, and its
	// body drained so the connection stays reusable.
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	upImage.learn(image)
	upImage.primary = upImage.fallback
	upImage.fallback = ""
	return alt, upImage, nil
}

// fetchFallbackForImage asks the upstream for the same path under the fallback
// name. It returns the response only when the fallback served the request —
// a fallback that does not answer is not an error, the primary answer stands.
func fetchFallbackForImage(ctx context.Context, repo *domain.Repository, upImage upstreamImage,
	suffix, rawQuery string, hdr http.Header,
) (*http.Response, bool) {
	altResp, err := repoproxy.FetchUpstreamOnce(ctx, repo, "/v2/"+upImage.fallback+suffix, rawQuery, hdr)
	if err != nil {
		return nil, false
	}
	if altResp.StatusCode != http.StatusOK && altResp.StatusCode != http.StatusNotModified {
		_ = altResp.Body.Close()
		return nil, false
	}
	return altResp, true
}

// dockerHubRemote returns the upstream URL of a Docker Hub proxy repository.
func dockerHubRemote(repo *domain.Repository) (string, bool) {
	if repo == nil || repo.Type != domain.TypeProxy {
		return "", false
	}
	remote, err := repoproxy.RemoteURL(repo)
	if err != nil || remote == "" {
		return "", false
	}
	if !repoproxy.IsDockerHubRemote(remote) {
		return "", false
	}
	return remote, true
}

// officialNames remembers the short image names a Docker Hub upstream serves
// under "library/". The mapping is per upstream, not per repository: two proxy
// repositories pointing at the same Hub share what Hub answered for a name.
type officialNameMap struct {
	mu    sync.Mutex
	names map[string]bool
}

var officialNames = &officialNameMap{names: map[string]bool{}}

// maxOfficialNames bounds what is remembered. A client can ask for any number
// of names; the mapping is an optimization (it saves the failed short-name
// lookup), not a correctness requirement, so the cap only limits what is
// remembered — a name past the cap still resolves, through the fallback.
const maxOfficialNames = 4096

// learn records that the upstream served this short name under "library/".
func (m *officialNameMap) learn(remote, image string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.names) >= maxOfficialNames {
		return
	}
	m.names[remote+"\x00"+image] = true
}

// knows reports whether the upstream already answered for this short name.
func (m *officialNameMap) knows(remote, image string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.names[remote+"\x00"+image]
}
