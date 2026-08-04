package conda

import (
	"net/url"
	"path"
	"strings"
)

// rewriteCondaURLs rewrites the "url" and "urls" fields inside "packages" and
// "packages.conda" of an upstream repodata.json so downloads route through this proxy.
//
// platform is the subdir the document was fetched from, i.e. the document lives at
// "<remoteBase>/<platform>/repodata.json" and its relative URLs resolve against
// "<remoteBase>/<platform>/". localBase is this proxy's CHANNEL root
// ("…/repository/<repo>/") and must end in "/", because an entry may name a sibling
// subdir and the proxy path has to be able to say so.
func rewriteCondaURLs(doc map[string]any, remoteBase, platform, localBase string) {
	for _, key := range []string{"packages", "packages.conda"} {
		pkgs, _ := doc[key].(map[string]any)
		for filename, v := range pkgs {
			entry, ok := v.(map[string]any)
			if !ok {
				continue
			}
			if u, ok := entry["url"].(string); ok {
				entry["url"] = rewritePackageURL(u, remoteBase, platform, localBase)
			}
			if urls, ok := entry["urls"].([]any); ok {
				for i, u := range urls {
					if s, ok := u.(string); ok {
						urls[i] = rewritePackageURL(s, remoteBase, platform, localBase)
					}
				}
				entry["urls"] = urls
			}
			pkgs[filename] = entry
		}
	}
}

// rewritePackageURL maps one repodata.json download URL onto this proxy.
//
// The URL is resolved as a reference against the directory holding repodata.json —
// "<remoteBase>/<platform>/" — the way any client resolving the document would, rather
// than pattern-matched. That leaves five shapes:
//
//  1. a relative path of any depth ("pkgs/numpy-1.24.0-py311_0.tar.bz2") — kept whole
//     under its subdir, because the download branch forwards the request path upstream
//     verbatim;
//  2. an absolute URL under the configured channel — the channel's own path prefix is
//     stripped and the remainder is treated as case 1;
//  3. a root-relative path ("/channel/linux-64/x.conda"), which resolves against the
//     HOST root and NOT the channel's path prefix — case 2 when it happens to land
//     inside the proxied channel, case 4 otherwise;
//  4. an absolute URL we cannot serve: another host (a CDN, the common case that
//     motivated this), a sibling subtree of the same host, or any URL carrying a query,
//     which the download branch would drop and so re-fetch unsigned;
//  5. a URL inside the channel but not under a subdir ("<channel>/x.tar.bz2"). Unlike
//     Helm, whose repository is flat, every conda request path is "/<subdir>/<file>",
//     so a single-segment path has no proxy URL that would route at all — it is case 4.
//
// Whatever falls to case 4 is handed back as the absolute upstream URL it resolves to,
// unchanged when the entry was already absolute. Such a package cannot be expressed as
// a path under this proxy, so the client fetches it directly: that skips the cache and
// needs egress, but it beats a proxy path that would 404 or, worse, quietly serve a
// different package.
//
// Note that unlike a Helm index, resolution is rooted at the DOCUMENT (the subdir),
// while the proxy path is rooted at the CHANNEL — an entry may legitimately name a
// sibling subdir, e.g. "../noarch/mypackage-0.1.0-py_0.conda".
func rewritePackageURL(rawURL, remoteBase, platform, localBase string) string {
	remote, err := url.Parse(remoteBase)
	if err != nil {
		return rawURL
	}
	ref, err := url.Parse(rawURL)
	if err != nil {
		return rawURL // unparsable: leave it for the client to deal with
	}

	// Resolve against the directory repodata.json itself lives in, with a trailing
	// slash, so a bare filename lands beside the index and a root-relative entry lands
	// at the host root.
	resolveBase := *remote
	resolveBase.Path = strings.TrimSuffix(remote.Path, "/") + "/" + platform + "/"
	resolveBase.RawPath = ""
	abs := resolveBase.ResolveReference(ref)

	// unproxyable hands the client the upstream URL. An entry that was already
	// absolute goes back byte for byte rather than as a re-serialized copy.
	unproxyable := func() string {
		if ref.Scheme != "" || ref.Host != "" {
			return rawURL
		}
		return abs.String()
	}

	// A query cannot survive the round trip: the download branch forwards only the
	// request path upstream, so a signed or tokenized URL would be re-fetched unsigned
	// and rejected. Send the client to the original instead.
	if abs.RawQuery != "" || abs.ForceQuery {
		return unproxyable()
	}
	if !sameUpstreamHost(abs, remote) {
		return unproxyable()
	}

	// Compare cleaned paths, so a ".." segment cannot slip past the subtree check and
	// only afterwards collapse into a path pointing at a different upstream file.
	remotePath := path.Clean("/" + strings.Trim(remote.Path, "/"))
	absPath := path.Clean("/" + strings.TrimPrefix(abs.Path, "/"))
	if remotePath != "/" {
		if absPath != remotePath && !strings.HasPrefix(absPath, remotePath+"/") {
			return unproxyable()
		}
		absPath = strings.TrimPrefix(absPath, remotePath)
	}
	rel := strings.TrimPrefix(absPath, "/")
	// Case 5: the conda routes only accept "/<subdir>/<file>".
	if !strings.Contains(rel, "/") {
		return unproxyable()
	}
	return localBase + rel
}

// sameUpstreamHost reports whether two URLs address the same upstream host. The
// scheme itself is ignored — an index served over https routinely lists http URLs and
// the reverse — but each scheme's default port is normalized away first, so an entry
// on "host:443" and a remote of bare "host" are recognized as one upstream.
func sameUpstreamHost(a, b *url.URL) bool {
	return normalizedHost(a, b.Scheme) == normalizedHost(b, a.Scheme)
}

// normalizedHost lowercases u's host and drops the port when it is the default for
// u's scheme. fallbackScheme supplies the scheme for a protocol-relative reference.
func normalizedHost(u *url.URL, fallbackScheme string) string {
	scheme := strings.ToLower(u.Scheme)
	if scheme == "" {
		scheme = strings.ToLower(fallbackScheme)
	}
	host := strings.ToLower(u.Host)
	switch scheme {
	case "http":
		return strings.TrimSuffix(host, ":80")
	case "https":
		return strings.TrimSuffix(host, ":443")
	}
	return host
}
