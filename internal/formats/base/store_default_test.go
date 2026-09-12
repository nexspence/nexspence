package base_test

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func TestStoreArtifact_ImplicitDefault_RoundtripUsesRegisteredStore(t *testing.T) {
	for _, assignment := range []*string{nil, new(""), new("  ")} {
		for _, group := range []bool{false, true} {
			name := "nil"
			if assignment != nil {
				name = "quoted-" + *assignment
			}
			if group {
				name += "-group"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				repo := testutil.SimpleRepo("implicit", "raw")
				repo.BlobStoreID = assignment
				d, _, _, _ := deps(repo)
				root := t.TempDir()
				fallback, err := storage.NewLocalBlobStore(root)
				require.NoError(t, err)
				d.BlobStore, d.Registry = fallback, storage.NewRegistry(fallback)
				target := &domain.BlobStore{ID: "target-id", Name: "default", Type: "local", Config: map[string]any{"path": filepath.Join(root, "default")}}
				d.Blobs = testutil.NewBlobStoreRepo(target)
				if group {
					target.Name = "member"
					d.Blobs = testutil.NewBlobStoreRepo(target, &domain.BlobStore{ID: "group-id", Name: "default", Type: "group", Config: map[string]any{"member_ids": []string{target.ID}, "fill_policy": "round_robin"}})
				}
				_, err = base.StoreArtifact(ctx, d, repo.Name, "/file", "text/plain", base.Coords{Name: "file"}, strings.NewReader("hello"), 5)
				require.NoError(t, err)
				rc, asset, err := base.FetchArtifact(ctx, d, repo.Name, "/file")
				require.NoError(t, err)
				body, err := io.ReadAll(rc)
				require.NoError(t, rc.Close())
				require.NoError(t, err)
				require.Equal(t, "hello", string(body))
				require.Equal(t, target.ID, asset.BlobStoreID)
				exists, err := fallback.Exists(ctx, asset.BlobKey)
				require.NoError(t, err)
				require.False(t, exists, "no bytes may be written to the global fallback")
				require.NoError(t, base.DeleteArtifact(ctx, d, repo.Name, "/file"))
				physical, err := base.PhysicalStore(ctx, d, target)
				require.NoError(t, err)
				exists, err = physical.Exists(ctx, asset.BlobKey)
				require.NoError(t, err)
				require.False(t, exists)
			})
		}
	}
}
