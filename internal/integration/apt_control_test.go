//go:build integration

package integration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// aptDeb builds a minimal real .deb (ar + debian-binary + control.tar.gz +
// data.tar.gz) around the given control file.
func aptDeb(t *testing.T, control, payload string) string {
	t.Helper()
	tgz := func(name, body string) []byte {
		var tb bytes.Buffer
		tw := tar.NewWriter(&tb)
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}))
		_, _ = tw.Write([]byte(body))
		require.NoError(t, tw.Close())
		var gb bytes.Buffer
		gw := gzip.NewWriter(&gb)
		_, _ = gw.Write(tb.Bytes())
		require.NoError(t, gw.Close())
		return gb.Bytes()
	}
	var ar bytes.Buffer
	ar.WriteString("!<arch>\n")
	member := func(name string, body []byte) {
		fmt.Fprintf(&ar, "%-16s%-12s%-6s%-6s%-8s%-10d`\n", name, "0", "0", "0", "100644", len(body))
		ar.Write(body)
		if len(body)%2 == 1 {
			ar.WriteByte('\n')
		}
	}
	member("debian-binary", []byte("2.0\n"))
	member("control.tar.gz", tgz("./control", control))
	member("data.tar.gz", tgz("./usr/share/doc/payload", payload))
	return ar.String()
}

// #637 through the real router and postgres: the stanza comes from the
// control file (multi-line fields survive the JSONB round trip), the control
// Architecture files the deb, one identity holds one file, and a path keeps
// the identity it was stored under until it is deleted.
func TestAptControlFileStanzas_RealShape(t *testing.T) {
	createHostedRepo(t, "apt", "apt-control", `{}`)
	token := login(t, "admin", "admin123")
	base := "/repository/apt-control"

	lib := "Package: libfoo2\nVersion: 2.3.1-1\nArchitecture: amd64\nMaintainer: Foo Developers <foo@example.org>\n" +
		"Depends: libfoo-common (= 2.3.1-1)\nProvides: libfoo-abi-2 (= 2.3.1-1)\nConflicts: libfoo1\n" +
		"Description: foo library\n First line.\n .\n Second paragraph.\n"
	libPath := base + "/pool/main/libfoo2_2.3.1-1+build5_amd64.deb"
	code, body := putBody(t, token, libPath, aptDeb(t, lib, "amd64"))
	require.Equal(t, http.StatusCreated, code, body)
	armLib := strings.Replace(lib, "Architecture: amd64", "Architecture: arm64", 1)
	code, body = putBody(t, token, base+"/pool/main/libfoo2_2.3.1-1_arm64.deb", aptDeb(t, armLib, "arm64"))
	require.Equal(t, http.StatusCreated, code, body)

	code, idx := getBody(t, token, base+"/dists/stable/main/binary-amd64/Packages")
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, idx, "Package: libfoo2\nVersion: 2.3.1-1\nArchitecture: amd64\n")
	assert.Contains(t, idx, "Depends: libfoo-common (= 2.3.1-1)\nProvides: libfoo-abi-2 (= 2.3.1-1)\nConflicts: libfoo1\n")
	assert.Contains(t, idx, "Description: foo library\n First line.\n .\n Second paragraph.\n")
	assert.NotContains(t, idx, "build5\n")
	assert.NotContains(t, idx, "Architecture: arm64", "arm64 has its own index")
	_, armIdx := getBody(t, token, base+"/dists/stable/main/binary-arm64/Packages")
	assert.Contains(t, armIdx, "Architecture: arm64\n")
	_, release := getBody(t, token, base+"/dists/stable/Release")
	assert.Contains(t, release, "Architectures: amd64 arm64 all\n")

	// One file per Package/Version/Architecture.
	code, body = putBody(t, token, base+"/pool/main/libfoo2_2.3.1-1+build6_amd64.deb", aptDeb(t, lib, "different bytes"))
	assert.Equal(t, http.StatusConflict, code, body)

	// A path keeps its identity: a redeploy naming another version is refused
	// and the stored file stays; after a delete the new version goes in.
	moved := strings.Replace(lib, "Version: 2.3.1-1", "Version: 2.3.2-1", 1)
	code, body = putBody(t, token, libPath, aptDeb(t, moved, "amd64 v2"))
	assert.Equal(t, http.StatusConflict, code, body)
	del := authReq(t, http.MethodDelete, libPath, nil, token)
	del.Body.Close()
	require.Less(t, del.StatusCode, 300)
	code, body = putBody(t, token, libPath, aptDeb(t, moved, "amd64 v2"))
	require.Equal(t, http.StatusCreated, code, body)
	_, idx = getBody(t, token, base+"/dists/stable/main/binary-amd64/Packages")
	assert.Contains(t, idx, "Version: 2.3.2-1\n")
	assert.NotContains(t, idx, "Version: 2.3.1-1\n")

	code, raw := getBody(t, token, "/service/rest/v1/components?repository=apt-control")
	require.Equal(t, http.StatusOK, code, raw)
	var page struct {
		Items []struct{ Group, Name, Version string } `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &page))
	var got []string
	for _, c := range page.Items {
		got = append(got, c.Group+"/"+c.Name+"/"+c.Version)
	}
	assert.Contains(t, got, "amd64/libfoo2/2.3.2-1")
	assert.Contains(t, got, "arm64/libfoo2/2.3.1-1")
}

// Concurrent claims of one identity under real advisory locks: exactly one
// upload wins, the rest get 409, and nothing deadlocks or exhausts the pool.
func TestAptControlConcurrentClaims_RealShape(t *testing.T) {
	createHostedRepo(t, "apt", "apt-control-race", `{}`)
	token := login(t, "admin", "admin123")
	ctl := "Package: foo\nVersion: 3.0\nArchitecture: amd64\nMaintainer: Foo Developers <foo@example.org>\nDescription: foo\n text\n"
	const n = 6
	debs := make([]string, n)
	for i := range debs {
		debs[i] = aptDeb(t, ctl, fmt.Sprint(i)) // built here: require must not run off the test goroutine
	}
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], _ = putBody(t, token, fmt.Sprintf("/repository/apt-control-race/pool/main/foo_3.0-try%d_amd64.deb", i), debs[i])
		}(i)
	}
	wg.Wait()
	created := 0
	for _, c := range codes {
		if c == http.StatusCreated {
			created++
		} else {
			assert.Equal(t, http.StatusConflict, c, "codes: %v", codes)
		}
	}
	assert.Equal(t, 1, created, "codes: %v", codes)
	_, idx := getBody(t, token, "/repository/apt-control-race/dists/stable/main/binary-amd64/Packages")
	assert.Equal(t, 1, strings.Count(idx, "Package: foo\n"))
}

// Uploads that are still streaming their body must not hold database
// connections: with more slow uploads in flight than the pool has
// connections, the rest of the server keeps answering. (An earlier draft
// held advisory-lock transactions for the whole upload and hung every
// request that needed the database.)
func TestAptControlSlowUploadsDoNotStarveThePool_RealShape(t *testing.T) {
	createHostedRepo(t, "apt", "apt-control-slow", `{}`)
	token := login(t, "admin", "admin123")
	n := int(pgtest.Pool(t).Config().MaxConns) + 2

	type upload struct {
		w    *io.PipeWriter
		tail string
		done chan int
	}
	uploads := make([]upload, n)
	for i := range uploads {
		ctl := fmt.Sprintf("Package: slow%d\nVersion: 1.0\nArchitecture: amd64\nMaintainer: Foo <foo@example.org>\nDescription: slow\n text\n", i)
		deb := aptDeb(t, ctl, strings.Repeat("x", 4096))
		cut := len(deb) - 16 // past the control member, inside data.tar
		pr, pw := io.Pipe()
		u := upload{w: pw, tail: deb[cut:], done: make(chan int, 1)}
		uploads[i] = u
		req, err := http.NewRequest(http.MethodPut, server(t).URL+fmt.Sprintf("/repository/apt-control-slow/pool/main/slow%d_1.0_amd64.deb", i), pr)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+token)
		req.ContentLength = int64(len(deb))
		go func() {
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				u.done <- 0
				return
			}
			resp.Body.Close()
			u.done <- resp.StatusCode
		}()
		go func(head string) { _, _ = io.WriteString(pw, head) }(deb[:cut])
	}

	// On failure, abort the stalled bodies so the server can shut down.
	t.Cleanup(func() {
		for _, u := range uploads {
			_ = u.w.CloseWithError(io.ErrUnexpectedEOF)
		}
	})

	// Every upload has sent its head and is now waiting for the rest.
	time.Sleep(500 * time.Millisecond)
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, server(t).URL+"/repository/apt-control-slow/dists/stable/Release", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	require.NoError(t, err, "the server must keep answering while %d uploads stream", n)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	for _, u := range uploads {
		_, _ = io.WriteString(u.w, u.tail)
		_ = u.w.Close()
	}
	for i, u := range uploads {
		assert.Equal(t, http.StatusCreated, <-u.done, "upload %d", i)
	}
}

// A pre-#637 deb re-pushed at its own path migrates in place on postgres,
// whose asset upsert keeps the row's component: the file is moved to its
// control coordinates explicitly and the emptied legacy component is dropped.
func TestAptControlLegacyRedeployMigrates_RealShape(t *testing.T) {
	createHostedRepo(t, "apt", "apt-control-legacy", `{}`)
	token := login(t, "admin", "admin123")
	p := "/repository/apt-control-legacy/pool/main/foo_1.0_amd64.deb"
	code, body := putBody(t, token, p, "legacy body, not an ar archive")
	require.Equal(t, http.StatusCreated, code, body)

	ctl := "Package: foo\nVersion: 1.0\nArchitecture: amd64\nMaintainer: Foo <foo@example.org>\nDepends: foo-common\nDescription: foo\n text\n"
	code, body = putBody(t, token, p, aptDeb(t, ctl, "real"))
	require.Equal(t, http.StatusCreated, code, body)

	_, idx := getBody(t, token, "/repository/apt-control-legacy/dists/stable/main/binary-amd64/Packages")
	assert.Contains(t, idx, "Depends: foo-common\n")
	assert.Equal(t, 1, strings.Count(idx, "Package: foo\n"))

	code, raw := getBody(t, token, "/service/rest/v1/components?repository=apt-control-legacy")
	require.Equal(t, http.StatusOK, code, raw)
	var page struct {
		Items []struct{ Group, Name, Version string } `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &page))
	require.Len(t, page.Items, 1, raw)
	assert.Equal(t, "amd64", page.Items[0].Group)
}
