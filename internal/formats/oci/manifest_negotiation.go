package oci

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

func readManifest(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	if len(body) > maxManifestBytes {
		return nil, fmt.Errorf("manifest exceeds the 4MiB limit")
	}
	return body, nil
}

func manifestContentType(body []byte, header string) (string, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return "", fmt.Errorf("manifest must be a JSON object")
	}
	var doc struct {
		MediaType string `json:"mediaType"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("invalid manifest JSON: %w", err)
	}
	ct, _, err := mime.ParseMediaType(header)
	if header != "" && err != nil {
		return "", fmt.Errorf("invalid manifest content type: %w", err)
	}
	if doc.MediaType != "" {
		// Missing/generic headers can be recovered from the document. Conflicting
		// concrete headers indicate mislabeled bytes, not a convertible format.
		if ct != "" && ct != "application/json" && ct != "application/octet-stream" && ct != doc.MediaType {
			return "", fmt.Errorf("manifest content type disagrees with body")
		}
		ct = doc.MediaType
	}
	if ct == "" {
		ct = "application/vnd.docker.distribution.manifest.v2+json"
	}
	return ct, nil
}

// Specific ranges take precedence over wildcards, including explicit q=0.
// Missing Accept permits the original bytes; malformed ranges do not grant
// permission. Digest bytes are never converted to satisfy another media type.
func acceptsManifest(accept, ct string) bool {
	return manifestQuality(accept, ct) > 0
}

func manifestQuality(accept, ct string) float64 {
	if strings.TrimSpace(accept) == "" {
		return 1
	}
	best, quality := -1, 0.0
	for _, part := range strings.Split(accept, ",") {
		mt, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		specificity := -1
		switch {
		case mt == ct:
			specificity = 2
		case mt == strings.SplitN(ct, "/", 2)[0]+"/*":
			specificity = 1
		case mt == "*/*":
			specificity = 0
		}
		if specificity < 0 {
			continue
		}
		q := 1.0
		if val, ok := params["q"]; ok {
			q, err = strconv.ParseFloat(val, 64)
			if err != nil || !(q >= 0 && q <= 1) {
				q = 0
			}
		}
		if specificity > best || (specificity == best && q > quality) {
			best, quality = specificity, q
		}
	}
	return quality
}

func prefersCachedManifest(accept, ct string) bool {
	quality := manifestQuality(accept, ct)
	for _, other := range []string{
		"application/vnd.docker.distribution.manifest.v2+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.oci.image.index.v1+json",
	} {
		if manifestQuality(accept, other) > quality {
			return false
		}
	}
	return quality > 0
}

func manifestAccept(c *gin.Context) string {
	return strings.Join(c.Request.Header.Values("Accept"), ",")
}
