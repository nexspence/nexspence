package repoproxy

import (
	"fmt"
	"net/http"
)

// tokenRedirectClient preserves the upstream client's transport, proxy and
// redirect policy without mutating the shared client. Go forwards Authorization
// to subdomains and ignores port changes and TLS downgrades on redirects; token
// credentials must obey the stricter trust boundary of SetUpstreamAuth.
func tokenRedirectClient(client *http.Client) *http.Client {
	cloned := *client
	cloned.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if client.CheckRedirect != nil {
			if err := client.CheckRedirect(req, via); err != nil {
				return err
			}
		} else if len(via) >= 10 {
			// Match net/http's default when the original client has no policy.
			return fmt.Errorf("stopped after 10 redirects")
		}
		if len(via) > 0 {
			previous := via[len(via)-1]
			if previous.Header.Get("Authorization") == "" ||
				!requestHostMatchesAny(req.URL, previous.URL.String()) {
				// Never restore credentials later in a redirect chain: net/http
				// may copy the initial request's headers again on the next hop.
				req.Header.Del("Authorization")
			}
		}
		return nil
	}
	return &cloned
}
