package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// directoryStub fakes the Admin SDK Directory groups.list endpoint.
type directoryStub struct {
	t        *testing.T
	pages    map[string]map[string]any // pageToken ("" = first) → response body
	status   int
	requests []*http.Request
}

func (d *directoryStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.requests = append(d.requests, r)
	if d.status != 0 {
		w.WriteHeader(d.status)
		_, _ = w.Write([]byte(`{"error":{"code":403,"message":"Not Authorized to access this resource/api"}}`))
		return
	}
	body, ok := d.pages[r.URL.Query().Get("pageToken")]
	require.True(d.t, ok, "unexpected pageToken %q", r.URL.Query().Get("pageToken"))
	_ = json.NewEncoder(w).Encode(body)
}

func newDirectoryUnderTest(t *testing.T, stub *directoryStub) *GoogleDirectory {
	t.Helper()
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.test"})
	return newGoogleDirectory(ts, srv.URL, srv.Client())
}

func TestGoogleDirectory_Groups_ReturnsGroupEmails(t *testing.T) {
	stub := &directoryStub{t: t, pages: map[string]map[string]any{
		"": {"groups": []map[string]any{
			{"email": "developers@company.com", "name": "Developers"},
			{"email": "nexspence-admins@company.com", "name": "Nexspence admins"},
		}},
	}}
	d := newDirectoryUnderTest(t, stub)

	groups, err := d.Groups(context.Background(), "alice@company.com")
	require.NoError(t, err)
	assert.Equal(t, []string{"developers@company.com", "nexspence-admins@company.com"}, groups)

	require.Len(t, stub.requests, 1)
	r := stub.requests[0]
	assert.Equal(t, "/admin/directory/v1/groups", r.URL.Path)
	assert.Equal(t, "alice@company.com", r.URL.Query().Get("userKey"))
	assert.Equal(t, "Bearer ya29.test", r.Header.Get("Authorization"))
}

func TestGoogleDirectory_Groups_FollowsPagination(t *testing.T) {
	stub := &directoryStub{t: t, pages: map[string]map[string]any{
		"":   {"groups": []map[string]any{{"email": "a@company.com"}}, "nextPageToken": "p2"},
		"p2": {"groups": []map[string]any{{"email": "b@company.com"}}},
	}}
	d := newDirectoryUnderTest(t, stub)

	groups, err := d.Groups(context.Background(), "alice@company.com")
	require.NoError(t, err)
	assert.Equal(t, []string{"a@company.com", "b@company.com"}, groups)
	assert.Len(t, stub.requests, 2)
}

func TestGoogleDirectory_Groups_NoMembership_IsEmptyNotError(t *testing.T) {
	// The API omits "groups" entirely for a user in no groups.
	stub := &directoryStub{t: t, pages: map[string]map[string]any{"": {"kind": "admin#directory#groups"}}}
	d := newDirectoryUnderTest(t, stub)

	groups, err := d.Groups(context.Background(), "alice@company.com")
	require.NoError(t, err)
	assert.Empty(t, groups)
}

func TestGoogleDirectory_Groups_APIError_IsError(t *testing.T) {
	stub := &directoryStub{t: t, status: http.StatusForbidden}
	d := newDirectoryUnderTest(t, stub)

	_, err := d.Groups(context.Background(), "alice@company.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
}

func TestNewGoogleDirectory_RejectsBadKey(t *testing.T) {
	_, err := NewGoogleDirectory(GoogleDirectoryConfig{ServiceAccountKey: "{not json", SubjectEmail: "admin@company.com"})
	require.Error(t, err)
}

func TestNewGoogleDirectory_RequiresSubject(t *testing.T) {
	_, err := NewGoogleDirectory(GoogleDirectoryConfig{ServiceAccountKey: `{"client_email":"sa@p.iam.gserviceaccount.com","private_key":"x","token_uri":"https://oauth2.googleapis.com/token"}`})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subject_email")
}
