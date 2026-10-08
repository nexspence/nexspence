//go:build integration

package integration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
