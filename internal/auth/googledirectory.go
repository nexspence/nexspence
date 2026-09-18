package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"
)

// googleDirectoryBaseURL is the Admin SDK Directory API root.
const googleDirectoryBaseURL = "https://admin.googleapis.com"

// googleDirectoryGroupScope is the read-only scope the service account must
// be granted domain-wide delegation for.
const googleDirectoryGroupScope = "https://www.googleapis.com/auth/admin.directory.group.readonly"

// googleDirectoryTimeout bounds one Directory API call; the lookup sits on
// the login path, so a slow Google must not hold a login for long.
const googleDirectoryTimeout = 10 * time.Second

// GoogleDirectoryConfig is what NewGoogleDirectory needs: a service-account
// key with domain-wide delegation and the Workspace user it impersonates.
type GoogleDirectoryConfig struct {
	ServiceAccountKey     string // JSON key, inline
	ServiceAccountKeyFile string // or a path to it; the inline value wins
	SubjectEmail          string // Workspace user with directory read rights
}

// GoogleDirectory implements GroupLookup against the Admin SDK Directory API
// (groups.list?userKey=<email>) using a delegated service account. It talks
// REST directly rather than through google.golang.org/api: one endpoint, one
// scope, and no extra module.
type GoogleDirectory struct {
	ts      oauth2.TokenSource
	client  *http.Client
	baseURL string
}

// NewGoogleDirectory builds the lookup from cfg. The service-account JSON is
// parsed here so a bad key fails at startup, not on the first login.
func NewGoogleDirectory(cfg GoogleDirectoryConfig) (*GoogleDirectory, error) {
	if cfg.SubjectEmail == "" {
		return nil, fmt.Errorf("google admin sdk: subject_email is required")
	}
	raw := []byte(cfg.ServiceAccountKey)
	if len(raw) == 0 {
		if cfg.ServiceAccountKeyFile == "" {
			return nil, fmt.Errorf("google admin sdk: service_account_key or service_account_key_file is required")
		}
		b, err := os.ReadFile(cfg.ServiceAccountKeyFile)
		if err != nil {
			return nil, fmt.Errorf("google admin sdk: read service account key: %w", err)
		}
		raw = b
	}
	var key struct {
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
		TokenURI    string `json:"token_uri"`
	}
	if err := json.Unmarshal(raw, &key); err != nil {
		return nil, fmt.Errorf("google admin sdk: service account key is not valid JSON: %w", err)
	}
	if key.ClientEmail == "" || key.PrivateKey == "" {
		return nil, fmt.Errorf("google admin sdk: service account key lacks client_email/private_key")
	}
	if key.TokenURI == "" {
		key.TokenURI = "https://oauth2.googleapis.com/token"
	}
	conf := &jwt.Config{
		Email:      key.ClientEmail,
		PrivateKey: []byte(key.PrivateKey),
		Scopes:     []string{googleDirectoryGroupScope},
		TokenURL:   key.TokenURI,
		Subject:    cfg.SubjectEmail, // domain-wide delegation: act as this user
	}
	ts := conf.TokenSource(context.Background())
	return newGoogleDirectory(ts, googleDirectoryBaseURL, &http.Client{Timeout: googleDirectoryTimeout}), nil
}

func newGoogleDirectory(ts oauth2.TokenSource, baseURL string, client *http.Client) *GoogleDirectory {
	return &GoogleDirectory{ts: oauth2.ReuseTokenSource(nil, ts), client: client, baseURL: baseURL}
}

// Groups lists the email addresses of every group the user belongs to,
// following nextPageToken until the listing ends.
func (d *GoogleDirectory) Groups(ctx context.Context, email string) ([]string, error) {
	tok, err := d.ts.Token()
	if err != nil {
		return nil, fmt.Errorf("google admin sdk: token: %w", err)
	}
	var out []string
	pageToken := ""
	for {
		q := url.Values{"userKey": {email}, "maxResults": {"200"}}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.baseURL+"/admin/directory/v1/groups?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		tok.SetAuthHeader(req)
		resp, err := d.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("google admin sdk: groups.list: %w", err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("google admin sdk: groups.list: read body: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("google admin sdk: groups.list: HTTP %d: %s", resp.StatusCode, truncate(body, 200))
		}
		var page struct {
			Groups []struct {
				Email string `json:"email"`
			} `json:"groups"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("google admin sdk: groups.list: decode: %w", err)
		}
		for _, g := range page.Groups {
			if g.Email != "" {
				out = append(out, g.Email)
			}
		}
		if page.NextPageToken == "" {
			return out, nil
		}
		pageToken = page.NextPageToken
	}
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
