//go:build integration

// Repository write policy (#539), end to end over the real router and a real
// PostgreSQL: the blob-key lock that serializes two concurrent first pushes is
// a Postgres advisory lock, which the unit tests only mirror with a mutex.
package integration

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createHostedRepo(t *testing.T, format, name, formatConfig string) {
	t.Helper()
	token := login(t, "admin", "admin123")
	body := fmt.Sprintf(`{"name":%q,"online":true,"formatConfig":%s}`, name, formatConfig)
	resp := authReq(t, http.MethodPost, "/service/rest/v1/repositories/"+format+"/hosted",
		strings.NewReader(body), token)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create %s hosted: %s", format, raw)
	t.Cleanup(func() {
		del := authReq(t, http.MethodDelete, "/service/rest/v1/repositories/"+name, nil, token)
		del.Body.Close()
	})
}

func putBody(t *testing.T, token, p, body string) (int, string) {
	t.Helper()
	resp := authReq(t, http.MethodPut, p, strings.NewReader(body), token)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func TestWritePolicy_RawAllowOnce_RealShape(t *testing.T) {
	createHostedRepo(t, "raw", "raw-write-once", `{"write_policy":"allow_once"}`)
	token := login(t, "admin", "admin123")
	p := "/repository/raw-write-once/releases/app-1.0.zip"

	code, _ := putBody(t, token, p, "first deploy")
	require.Equal(t, http.StatusCreated, code)

	code, body := putBody(t, token, p, "second deploy")
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body, "Repository does not allow updating assets: raw-write-once")
	assert.Equal(t, "first deploy", fetchOK(t, p))

	// Two concurrent first pushes of a new path: exactly one may win, and the
	// stored bytes are the winner's.
	racePath := "/repository/raw-write-once/releases/app-2.0.zip"
	const n = 6
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], _ = putBody(t, token, racePath, fmt.Sprintf("pusher-%d", i))
		}(i)
	}
	wg.Wait()
	created, winner := 0, -1
	for i, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
			winner = i
		case http.StatusBadRequest:
		default:
			t.Errorf("pusher %d: unexpected status %d", i, c)
		}
	}
	require.Equal(t, 1, created, "statuses %v: exactly one first push may succeed", codes)
	assert.Equal(t, fmt.Sprintf("pusher-%d", winner), fetchOK(t, racePath))
}

func TestWritePolicy_ReadOnlyAndValidation_RealShape(t *testing.T) {
	token := login(t, "admin", "admin123")

	// An unknown policy is refused at create.
	resp := authReq(t, http.MethodPost, "/service/rest/v1/repositories/raw/hosted",
		strings.NewReader(`{"name":"raw-bad-policy","online":true,"formatConfig":{"write_policy":"sometimes"}}`), token)
	resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	createHostedRepo(t, "raw", "raw-read-only", `{"write_policy":"allow"}`)
	code, _ := putBody(t, token, "/repository/raw-read-only/a.txt", "a")
	require.Equal(t, http.StatusCreated, code)

	// Switch it to read-only through the update API.
	up := authReq(t, http.MethodPut, "/service/rest/v1/repositories/raw/hosted/raw-read-only",
		strings.NewReader(`{"online":true,"formatConfig":{"write_policy":"deny"}}`), token)
	up.Body.Close()
	require.Equal(t, http.StatusOK, up.StatusCode)

	code, body := putBody(t, token, "/repository/raw-read-only/b.txt", "b")
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body, "Repository is read-only: raw-read-only")
	code, _ = putBody(t, token, "/repository/raw-read-only/a.txt", "a2")
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "a", fetchOK(t, "/repository/raw-read-only/a.txt"))
}
