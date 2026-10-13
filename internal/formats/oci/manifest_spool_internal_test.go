package oci

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManifestBody_SpoolAndCleanup(t *testing.T) {
	raw := `{"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"fallback"},"artifactType":"explicit","subject":{"digest":"sha256:abc"},"annotations":{"escaped":"a\/b\uD83D\uDE00"},"padding":"` + strings.Repeat("x", 5<<20) + `"}`
	body, err := spoolManifest(strings.NewReader(raw))
	require.NoError(t, err)
	name := body.file.Name()
	defer body.Close()
	require.Equal(t, int64(len(raw)), body.size)
	require.Equal(t, "explicit", body.meta.ArtifactType)
	require.Equal(t, "sha256:abc", body.meta.Subject)
	require.Equal(t, "a/b😀", body.meta.Annotations["escaped"])
	stored, err := io.ReadAll(body.reader())
	require.NoError(t, err)
	require.Equal(t, raw, string(stored))
	body.Close()
	_, err = os.Stat(name)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestManifestBody_InvalidSpoolRemovesTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	for _, r := range []io.Reader{brokenManifestReader{}, strings.NewReader(`{"padding":"` + strings.Repeat("x", 5<<20) + `\q"}`)} {
		_, err := spoolManifest(r)
		require.Error(t, err)
		files, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Empty(t, files)
	}
}

func TestManifestJSON_Validation(t *testing.T) {
	for _, raw := range []string{
		`{}`, ` {"x":[null,true,false,{},[],0,-12,1.25,1e3,-2.3E-4]} `,
		`{"mediaType":null}`, `{"mediaType":true}`, `{"mediaType":[]}`,
		`{"x":01}`, `{"x":-}`, `{"x":1.}`, `{"x":1e+}`, `{"x":1e}`, `{"x":+1}`, `{"x":1x}`,
		`{"x":nul}`, `{"x":fals}`, `{"x":tru}`, `{"x":"\uD800"}`, `{"x":"\uZZZZ"}`,
		`{"x":"\q"}`, "{\"x\":\"\n\"}", `{"x":[1,]}`, `{"x":{},}`, `{"x" 1}`, `{} {}`,
		`{"x":"\/\b\f\n\r\t\\\""}`, `{"x": [ [ {} ] ] }`,
	} {
		t.Run(raw, func(t *testing.T) {
			var expected struct {
				MediaType string `json:"mediaType"`
			}
			want := json.Unmarshal([]byte(raw), &expected) == nil
			parser := manifestJSON{r: bufio.NewReader(strings.NewReader(raw))}
			require.Equal(t, want, parser.document() == nil)
		})
	}
}

func FuzzManifestJSON_Validation(f *testing.F) {
	for _, seed := range []string{`null`, `true`, `false`, `0`, `-123.4e+5`, `"\/\uD800"`, `[{},[],null]`, `{"x":[1,2]}`, `[1,]`, `"\q"`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		raw := `{"ignored":` + value + `}`
		parser := manifestJSON{r: bufio.NewReader(strings.NewReader(raw))}
		if got, want := parser.document() == nil, json.Valid([]byte(raw)); got != want {
			t.Fatalf("validation=%v, json.Valid=%v for %q", got, want, raw)
		}
	})
}
