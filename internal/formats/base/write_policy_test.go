package base_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func policyRepo(name, format string, policy domain.WritePolicy) *domain.Repository {
	repo := testutil.SimpleRepo(name, format)
	repo.FormatConfig = map[string]any{domain.WritePolicyKey: string(policy)}
	return repo
}

func put(ctx context.Context, d formats.Deps, repo, p, body string) error {
	_, err := base.StoreArtifact(ctx, d, repo, p, "application/octet-stream",
		base.Coords{Name: "x", Version: "1"}, strings.NewReader(body), int64(len(body)))
	return err
}

func TestWritePolicy_AllowOnce_FirstWriteOKSecondRejected(t *testing.T) {
	repo := policyRepo("once", "raw", domain.WritePolicyAllowOnce)
	d, blobStore, _, _ := deps(repo)
	ctx := context.Background()

	require.NoError(t, put(ctx, d, "once", "/lib/app-1.0.jar", "original"))

	err := put(ctx, d, "once", "/lib/app-1.0.jar", "replacement!")
	require.ErrorIs(t, err, base.ErrRedeployDenied)
	assert.Equal(t, "Repository does not allow updating assets: once", err.Error())
	assert.Equal(t, http.StatusBadRequest, base.HTTPStatusForError(err))

	// The refusal came before Put: the first deploy's bytes are untouched.
	got, rerr := blobStore.Read(base.BlobKey("once", "/lib/app-1.0.jar"))
	require.NoError(t, rerr)
	assert.Equal(t, "original", got)

	// A different path is still a first deploy.
	require.NoError(t, put(ctx, d, "once", "/lib/app-1.1.jar", "next"))
}

func TestWritePolicy_Allow_IsTheDefaultAndReplaces(t *testing.T) {
	for _, repo := range []*domain.Repository{
		testutil.SimpleRepo("plain", "raw"),
		policyRepo("plain", "raw", domain.WritePolicyAllow),
	} {
		d, blobStore, _, _ := deps(repo)
		ctx := context.Background()
		require.NoError(t, put(ctx, d, "plain", "/f", "one"))
		require.NoError(t, put(ctx, d, "plain", "/f", "two"))
		got, _ := blobStore.Read(base.BlobKey("plain", "/f"))
		assert.Equal(t, "two", got)
	}
}

func TestWritePolicy_Deny_RejectsEveryWrite(t *testing.T) {
	repo := policyRepo("ro", "maven2", domain.WritePolicyDeny)
	d, blobStore, _, assets := deps(repo)
	ctx := context.Background()

	for _, p := range []string{"/g/a/1.0/a-1.0.jar", "/g/a/maven-metadata.xml", "/g/a/1.0-SNAPSHOT/a.jar"} {
		err := put(ctx, d, "ro", p, "bytes")
		require.ErrorIs(t, err, base.ErrRepositoryReadOnly, p)
		assert.Equal(t, "Repository is read-only: ro", err.Error())
		assert.Equal(t, http.StatusBadRequest, base.HTTPStatusForError(err))
		assert.False(t, blobStore.Has(base.BlobKey("ro", p)), "nothing may be written for %s", p)
	}
	list, _ := assets.List(ctx, "", 100, 0)
	assert.Empty(t, list.Items)
}

func TestWritePolicy_NotAppliedToNonHosted(t *testing.T) {
	repo := policyRepo("px", "raw", domain.WritePolicyDeny)
	repo.Type = domain.TypeProxy
	d, _, _, _ := deps(repo)
	require.NoError(t, put(context.Background(), d, "px", "/f", "cached"))
}

func TestWritePolicy_WithoutWritePolicy_BypassesDenyAndAllowOnce(t *testing.T) {
	for _, pol := range []domain.WritePolicy{domain.WritePolicyDeny, domain.WritePolicyAllowOnce} {
		repo := policyRepo("mig", "raw", pol)
		d, blobStore, _, _ := deps(repo)
		ctx := base.WithoutWritePolicy(context.Background())
		require.NoError(t, put(ctx, d, "mig", "/f", "one"))
		require.NoError(t, put(ctx, d, "mig", "/f", "two"))
		got, _ := blobStore.Read(base.BlobKey("mig", "/f"))
		assert.Equal(t, "two", got)
	}
}

func TestWritePolicy_AllowOnce_MavenMetadataAndSnapshotsExempt(t *testing.T) {
	repo := policyRepo("mvn", "maven2", domain.WritePolicyAllowOnce)
	d, blobStore, _, _ := deps(repo)
	ctx := context.Background()

	for _, p := range []string{
		"/com/acme/app/maven-metadata.xml",
		"/com/acme/app/1.0-SNAPSHOT/maven-metadata.xml",
		"/com/acme/app/1.0-SNAPSHOT/app-1.0-SNAPSHOT.jar",
		"/com/acme/app/1.0-SNAPSHOT/app-1.0-20260925.101010-1.jar",
	} {
		require.NoError(t, put(ctx, d, "mvn", p, "v1"), p)
		require.NoError(t, put(ctx, d, "mvn", p, "v2"), p)
		got, _ := blobStore.Read(base.BlobKey("mvn", p))
		assert.Equal(t, "v2", got, p)
	}

	// A release artifact stays write-once.
	rel := "/com/acme/app/1.0/app-1.0.jar"
	require.NoError(t, put(ctx, d, "mvn", rel, "v1"))
	require.ErrorIs(t, put(ctx, d, "mvn", rel, "v2"), base.ErrRedeployDenied)
	// So is a file whose name merely mentions SNAPSHOT outside a SNAPSHOT dir.
	odd := "/com/acme/app/1.0/app-1.0-SNAPSHOT-notes.txt"
	require.NoError(t, put(ctx, d, "mvn", odd, "v1"))
	require.ErrorIs(t, put(ctx, d, "mvn", odd, "v2"), base.ErrRedeployDenied)
}

func TestRedeployExempt(t *testing.T) {
	docker := policyRepo("d", "docker", domain.WritePolicyAllowOnce)
	dockerLatest := policyRepo("d", "docker", domain.WritePolicyAllowOnce)
	dockerLatest.FormatConfig[domain.AllowRedeployLatestKey] = true
	oci := policyRepo("o", "oci", domain.WritePolicyAllowOnce)
	oci.FormatConfig[domain.AllowRedeployLatestKey] = true
	raw := policyRepo("r", "raw", domain.WritePolicyAllowOnce)
	raw.FormatConfig[domain.AllowRedeployLatestKey] = true

	cases := []struct {
		name string
		repo *domain.Repository
		path string
		want bool
	}{
		{"nil repo", nil, "/x", false},
		{"docker tag", docker, "/manifests/app/1.0", false},
		{"docker latest without flag", docker, "/manifests/app/latest", false},
		{"docker latest with flag", dockerLatest, "/manifests/team/app/latest", true},
		{"docker other tag with flag", dockerLatest, "/manifests/app/1.0", false},
		{"docker digest manifest", docker, "/manifests/app/sha256:abc", true},
		{"docker blob", docker, "/blobs/app/sha256:abc", true},
		{"oci latest with flag", oci, "/manifests/chart/latest", true},
		{"raw path named latest", raw, "/manifests/app/latest", false},
		{"raw blob-looking path", raw, "/blobs/x", false},
		{"maven metadata", policyRepo("m", "maven2", domain.WritePolicyAllowOnce), "/g/a/maven-metadata.xml", true},
		{"maven release", policyRepo("m", "maven2", domain.WritePolicyAllowOnce), "/g/a/1.0/a-1.0.pom", false},
		{"npm tarball", policyRepo("n", "npm", domain.WritePolicyAllowOnce), "/pkg/-/pkg-1.0.0.tgz", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, base.RedeployExempt(tc.repo, tc.path))
		})
	}
}

func TestWritePolicy_Docker_LatestAndDigestExemptTagsWriteOnce(t *testing.T) {
	repo := policyRepo("reg", "docker", domain.WritePolicyAllowOnce)
	d, _, _, _ := deps(repo)
	ctx := context.Background()

	require.NoError(t, put(ctx, d, "reg", "/manifests/app/1.0", "m1"))
	require.ErrorIs(t, put(ctx, d, "reg", "/manifests/app/1.0", "m2"), base.ErrRedeployDenied)

	// latest is write-once too until the repository opts in.
	require.NoError(t, put(ctx, d, "reg", "/manifests/app/latest", "m1"))
	require.ErrorIs(t, put(ctx, d, "reg", "/manifests/app/latest", "m2"), base.ErrRedeployDenied)

	repo.FormatConfig[domain.AllowRedeployLatestKey] = true
	require.NoError(t, put(ctx, d, "reg", "/manifests/app/latest", "m2"))
	require.ErrorIs(t, put(ctx, d, "reg", "/manifests/app/1.0", "m3"), base.ErrRedeployDenied)

	// Content-addressed paths may be written again: the bytes are the same.
	for _, p := range []string{"/manifests/app/sha256:aa", "/blobs/app/sha256:bb"} {
		require.NoError(t, put(ctx, d, "reg", p, "same"))
		require.NoError(t, put(ctx, d, "reg", p, "same"))
	}
}

func TestWritePolicy_LookupFailureFailsClosed(t *testing.T) {
	repo := policyRepo("once", "raw", domain.WritePolicyAllowOnce)
	d, blobStore, _, assets := deps(repo)
	assets.GetByPathErr = errors.New("db down")
	err := put(context.Background(), d, "once", "/f", "x")
	require.Error(t, err)
	assert.NotErrorIs(t, err, base.ErrRedeployDenied)
	assert.False(t, blobStore.Has(base.BlobKey("once", "/f")))
}

func TestCheckWritable(t *testing.T) {
	assert.NoError(t, base.CheckWritable(testutil.SimpleRepo("a", "docker")))
	assert.NoError(t, base.CheckWritable(policyRepo("a", "docker", domain.WritePolicyAllowOnce)))
	assert.ErrorIs(t, base.CheckWritable(policyRepo("a", "docker", domain.WritePolicyDeny)), base.ErrRepositoryReadOnly)
}

// gatedReader blocks its first Read until release is closed, signaling
// started once the write is under way (i.e. past the policy check and inside
// Put).
type gatedReader struct {
	r       io.Reader
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gatedReader) Read(p []byte) (int, error) {
	g.once.Do(func() {
		close(g.started)
		<-g.release
	})
	return g.r.Read(p)
}

// Two concurrent first pushes of one path: the second must see the first's
// row once the first finishes, instead of both passing an existence check
// taken before either registered.
func TestWritePolicy_AllowOnce_ConcurrentFirstPushes_OnlyOneWins(t *testing.T) {
	repo := policyRepo("race", "raw", domain.WritePolicyAllowOnce)
	d, blobStore, _, _ := deps(repo)
	ctx := context.Background()
	const p = "/lib/app-1.0.jar"

	first := &gatedReader{r: strings.NewReader("first"), started: make(chan struct{}), release: make(chan struct{})}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, errs[0] = base.StoreArtifact(ctx, d, "race", p, "application/octet-stream",
			base.Coords{Name: "app", Version: "1.0"}, first, 5)
	}()
	<-first.started // the first push passed the check and is writing

	wg.Add(1)
	go func() {
		defer wg.Done()
		// The path has no row yet, so this push passes the unlocked check and
		// then has to wait for the first one's lock.
		_, errs[1] = base.StoreArtifact(ctx, d, "race", p, "application/octet-stream",
			base.Coords{Name: "app", Version: "1.0"}, strings.NewReader("other"), 5)
	}()
	time.Sleep(50 * time.Millisecond)
	close(first.release)
	wg.Wait()

	require.NoError(t, errs[0])
	require.ErrorIs(t, errs[1], base.ErrRedeployDenied)
	got, _ := blobStore.Read(base.BlobKey("race", p))
	assert.Equal(t, "first", got)
}
