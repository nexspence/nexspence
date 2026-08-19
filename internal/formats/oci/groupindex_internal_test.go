package oci

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The imageless /v2/<repoName>/tags/list is not mergeable: ServeHTTP refuses
// it with NAME_UNKNOWN, and the classifier must mirror that rather than
// promising a tags document no member would produce.
func TestGroupIndexKind_ImagelessTagsListIsNotMergeable(t *testing.T) {
	kind, image := groupIndexKind("/v2/tags/list")
	assert.Equal(t, indexNone, kind)
	assert.Empty(t, image)

	kind, image = groupIndexKind("/v2/myapp/tags/list")
	assert.Equal(t, indexTags, kind)
	assert.Equal(t, "myapp", image)
}
