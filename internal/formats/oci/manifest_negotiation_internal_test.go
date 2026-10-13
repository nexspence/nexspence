package oci

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestAcceptsManifest_RangesAndQuality(t *testing.T) {
	const ct = "application/vnd.oci.image.index.v1+json"
	for _, tc := range []struct {
		accept string
		want   bool
	}{
		{"", true}, {"*/*", true}, {"application/*", true}, {ct, true},
		{ct + ";q=0.5", true}, {ct + ";q=0, */*;q=1", false},
		{"application/*;q=0, */*", false}, {"text/plain", false},
		{ct + ";q=broken", false}, {ct + ";q=NaN", false}, {ct + ";q=2", false},
		{ct + ";q=-1", false}, {"%%%", false},
		{ct + ";q=0.1, " + ct + ";q=0.9", true},
	} {
		t.Run(tc.accept, func(t *testing.T) {
			if got := acceptsManifest(tc.accept, ct); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestManifestContentType_VerifiesDocument(t *testing.T) {
	const ct = "application/vnd.oci.image.manifest.v1+json"
	for _, tc := range []struct {
		name, body, header, want string
		bad                      bool
	}{
		{"original", `{"mediaType":"` + ct + `"}`, ct, ct, false},
		{"missing header", `{"mediaType":"` + ct + `"}`, "", ct, false},
		{"generic header", `{"mediaType":"` + ct + `"}`, "application/json", ct, false},
		{"parameters", `{}`, ct + "; charset=utf-8", ct, false},
		{"default", `{}`, "", "application/vnd.docker.distribution.manifest.v2+json", false},
		{"invalid JSON", `{`, ct, "", true},
		{"invalid header", `{}`, "bad header", "", true},
		{"mislabeled", `{"mediaType":"` + ct + `"}`, "application/vnd.docker.distribution.manifest.v2+json", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := manifestContentType([]byte(tc.body), tc.header)
			if (err != nil) != tc.bad || got != tc.want {
				t.Errorf("got %q, %v", got, err)
			}
		})
	}
}

type brokenManifestReader struct{}

func (brokenManifestReader) Read([]byte) (int, error) { return 0, errors.New("connection lost") }
func TestReadManifest_RejectsReadErrorsAndOverflow(t *testing.T) {
	for _, reader := range []io.Reader{brokenManifestReader{}, strings.NewReader(strings.Repeat("x", maxManifestBytes+1))} {
		if _, err := readManifest(reader); err == nil {
			t.Error("expected read failure")
		}
	}
}
