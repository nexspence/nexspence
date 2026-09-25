package base_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// recordingPublishes captures what the storage layer reports as publishes.
type recordingPublishes struct {
	mu   sync.Mutex
	seen []string
}

func (r *recordingPublishes) NotifyPublished(_ context.Context, repoName, componentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, repoName+"/"+componentID)
}

func (r *recordingPublishes) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.seen)
}

// Only client publishes into hosted repositories reach auto-promotion (#542),
// and for a registry only a manifest pushed by tag: layers and manifests pushed
// by digest are parts of the image the tag promotes.
func TestStoreArtifact_NotifiesPublishes(t *testing.T) {
	store := func(t *testing.T, ctx context.Context, repo *domain.Repository, path string) int {
		t.Helper()
		d, _, _, _ := deps(repo)
		pub := &recordingPublishes{}
		d.Publishes = pub
		_, err := base.StoreArtifact(ctx, d, repo.Name, path, "application/octet-stream",
			base.Coords{Name: "x", Version: "1"}, strings.NewReader("body"), 4)
		require.NoError(t, err)
		return pub.count()
	}
	ctx := context.Background()
	hosted := func(format string) *domain.Repository { return testutil.SimpleRepo("r", format) }
	proxy := testutil.SimpleRepo("p", "raw")
	proxy.Type = domain.TypeProxy

	cases := []struct {
		name string
		ctx  context.Context
		repo *domain.Repository
		path string
		want int
	}{
		{"raw upload", ctx, hosted("raw"), "/a.txt", 1},
		{"proxy cache fill", ctx, proxy, "/a.txt", 0},
		{"migration write", base.WithoutWritePolicy(ctx), hosted("raw"), "/a.txt", 0},
		{"docker tag", ctx, hosted("docker"), "/manifests/team/app/1.0", 1},
		{"docker digest manifest", ctx, hosted("docker"), "/manifests/team/app/sha256:abc", 0},
		{"docker blob", ctx, hosted("docker"), "/blobs/team/app/sha256:abc", 0},
		{"oci malformed manifest path", ctx, hosted("oci"), "/manifests/app", 0},
		{"oci trailing slash", ctx, hosted("oci"), "/manifests/app/", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, store(t, tc.ctx, tc.repo, tc.path))
		})
	}
}
