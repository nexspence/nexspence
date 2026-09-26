package helm_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/helm"
)

func TestHelm_GroupIndexSourcePath(t *testing.T) {
	h := helm.New(formats.Deps{})

	src, ok := h.GroupIndexSourcePath("/index.yaml")
	require.True(t, ok)
	assert.Equal(t, "/index.yaml", src)

	src, ok = h.GroupIndexSourcePath("index.yaml")
	require.True(t, ok, "unprefixed path still matches after norm")
	assert.Equal(t, "/index.yaml", src)

	_, ok = h.GroupIndexSourcePath("/nginx-1.0.0.tgz")
	assert.False(t, ok, "chart archives keep first-non-404")
}

func TestHelm_MergeGroupIndex_UnionsCharts(t *testing.T) {
	h := helm.New(formats.Deps{BaseURL: "http://localhost:8080"})
	hosted := []byte(`
apiVersion: v1
entries:
  widget:
    - name: widget
      version: 1.2.3
      urls:
        - http://localhost:8080/repository/helm-hosted/widget-1.2.3.tgz
`)
	proxy := []byte(`
apiVersion: v1
entries:
  ingress:
    - name: ingress
      version: 4.0.0
      urls:
        - http://localhost:8080/repository/helm-remote/ingress-4.0.0.tgz
  widget:
    - name: widget
      version: 1.2.3
      urls:
        - http://localhost:8080/repository/helm-remote/widget-1.2.3.tgz
`)

	body, ct, err := h.MergeGroupIndex("helm", "/index.yaml", []formats.GroupIndexPart{
		{Member: "helm-hosted", Body: hosted},
		{Member: "helm-remote", Body: proxy},
	})
	require.NoError(t, err)
	assert.Equal(t, "application/yaml", ct)

	var doc struct {
		Entries map[string][]map[string]any `yaml:"entries"`
	}
	require.NoError(t, yaml.Unmarshal(body, &doc))
	require.Contains(t, doc.Entries, "widget")
	require.Contains(t, doc.Entries, "ingress")
	require.Len(t, doc.Entries["widget"], 1, "same name+version: first member wins")

	hostedURL, _ := doc.Entries["widget"][0]["urls"].([]any)
	require.Len(t, hostedURL, 1)
	assert.Equal(t, "http://localhost:8080/repository/helm/widget-1.2.3.tgz", hostedURL[0],
		"member-minted URL re-rooted at the group")

	remoteURL, _ := doc.Entries["ingress"][0]["urls"].([]any)
	require.Len(t, remoteURL, 1)
	assert.Equal(t, "http://localhost:8080/repository/helm/ingress-4.0.0.tgz", remoteURL[0],
		"member-minted URL (including GitHub origins rewritten by the proxy) re-rooted at the group")
}

func TestHelm_MergeGroupIndex_MalformedPartSkipped(t *testing.T) {
	h := helm.New(formats.Deps{})
	body, _, err := h.MergeGroupIndex("g", "/index.yaml", []formats.GroupIndexPart{
		{Member: "bad", Body: []byte("::::")},
		{Member: "ok", Body: []byte("apiVersion: v1\nentries:\n  x:\n    - name: x\n      version: \"1.0.0\"\n")},
	})
	require.NoError(t, err)
	assert.Contains(t, string(body), "name: x")
}

func TestHelm_MergeGroupIndex_EmptyHostedDoesNotHideProxy(t *testing.T) {
	h := helm.New(formats.Deps{})
	emptyHosted := []byte("apiVersion: v1\nentries: {}\ngenerated: \"2026-01-01T00:00:00Z\"\n")
	proxy := []byte("apiVersion: v1\nentries:\n  redis:\n    - name: redis\n      version: \"1.0.0\"\n")
	body, _, err := h.MergeGroupIndex("helm", "/index.yaml", []formats.GroupIndexPart{
		{Member: "helm-hosted", Body: emptyHosted},
		{Member: "helm-proxy", Body: proxy},
	})
	require.NoError(t, err)
	assert.Contains(t, string(body), "redis")
}
