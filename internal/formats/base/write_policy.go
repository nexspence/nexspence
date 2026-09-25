package base

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// ErrRedeployDenied is returned when a client write would replace an asset in a
// hosted repository whose write policy is allow_once ("Disable redeploy").
// The wording is Nexus's, which Maven, Gradle and npm users already recognize.
var ErrRedeployDenied = errors.New("Repository does not allow updating assets") //nolint:staticcheck,revive // client-facing message, kept verbatim from Nexus

// ErrRepositoryReadOnly is returned for any client write to a hosted repository
// whose write policy is deny ("Read-only").
var ErrRepositoryReadOnly = errors.New("Repository is read-only") //nolint:staticcheck,revive // client-facing message

type writePolicyBypassKey struct{}

// WithoutWritePolicy marks ctx as a write that is not a client deploy — a Nexus
// migration bringing content across — so StoreArtifact skips the repository's
// write policy. Nothing reachable from an HTTP request may set it.
func WithoutWritePolicy(ctx context.Context) context.Context {
	return context.WithValue(ctx, writePolicyBypassKey{}, true)
}

func writePolicyBypassed(ctx context.Context) bool {
	v, _ := ctx.Value(writePolicyBypassKey{}).(bool)
	return v
}

// RedeployExempt reports whether a write to filePath may replace an existing
// asset even under allow_once. These are paths the format itself rewrites by
// design, or whose content is fixed by their name:
//
//   - maven2: maven-metadata.xml at any level, and every file inside a
//     -SNAPSHOT version directory — Maven rewrites both on each deploy;
//   - docker/oci: blobs and manifests addressed by digest (the bytes are
//     identical by definition), and the "latest" tag when the repository sets
//     allow_redeploy_latest.
//
// A read-only (deny) repository rejects exempt paths too.
func RedeployExempt(repo *domain.Repository, filePath string) bool {
	if repo == nil {
		return false
	}
	switch {
	case repo.Format == domain.FormatMaven2:
		if path.Base(filePath) == "maven-metadata.xml" {
			return true
		}
		return strings.HasSuffix(path.Base(path.Dir(filePath)), "-SNAPSHOT")
	case repo.Format.IsOCIRegistry():
		if strings.HasPrefix(filePath, "/blobs/") {
			return true
		}
		if strings.HasPrefix(filePath, "/manifests/") {
			ref := path.Base(filePath)
			if strings.Contains(ref, ":") {
				return true
			}
			return ref == "latest" && domain.RepoAllowsRedeployLatest(repo)
		}
	}
	return false
}

// CheckWritable rejects every write to a read-only hosted repository. It is the
// part of the policy that does not depend on the path, for callers that want
// to refuse before they start a multi-step upload.
func CheckWritable(repo *domain.Repository) error {
	if domain.RepoWritePolicy(repo) == domain.WritePolicyDeny {
		return fmt.Errorf("%w: %s", ErrRepositoryReadOnly, repo.Name)
	}
	return nil
}

// CheckWritePolicy answers whether a write of filePath into repo is allowed
// right now: a read-only repository refuses everything, a disable-redeploy one
// refuses a path that already holds an asset unless the path is exempt.
// It reports whether the caller must serialize its write per blob key (see
// StoreArtifact) — true exactly when the answer depends on the path not
// existing yet. A lookup that fails for any reason other than "not found"
// fails closed.
func CheckWritePolicy(ctx context.Context, assets repository.AssetRepo, repo *domain.Repository, filePath string) (guarded bool, err error) {
	switch domain.RepoWritePolicy(repo) {
	case domain.WritePolicyDeny:
		return false, fmt.Errorf("%w: %s", ErrRepositoryReadOnly, repo.Name)
	case domain.WritePolicyAllowOnce:
		if RedeployExempt(repo, filePath) {
			return false, nil
		}
		if err := assetAbsent(ctx, assets, repo, filePath); err != nil {
			return true, err
		}
		return true, nil
	}
	return false, nil
}

// assetAbsent returns ErrRedeployDenied when filePath already holds an asset.
func assetAbsent(ctx context.Context, assets repository.AssetRepo, repo *domain.Repository, filePath string) error {
	existing, err := assets.GetByPath(ctx, repo.Name, filePath)
	if errors.Is(err, repository.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check existing asset: %w", err)
	}
	if existing == nil {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrRedeployDenied, repo.Name)
}

// enforceWritePolicy runs write under repo's write policy for a client write
// of filePath. Under allow_once the existence check and the write run under
// the path's blob-key lock, so two concurrent first pushes of one path cannot
// both pass the check: the second sees the first's row and is refused before
// it touches the bytes. The key is the one StoreArtifact writes to, and the
// locks RegisterStoredBlob takes inside are the differently named quota locks,
// so the nesting order is always blob key → quota and cannot deadlock.
func enforceWritePolicy(ctx context.Context, d formats.Deps, repo *domain.Repository, filePath, blobKey string, write func(context.Context) error) error {
	if writePolicyBypassed(ctx) {
		return write(ctx)
	}
	guarded, err := CheckWritePolicy(ctx, d.Assets, repo, filePath)
	if err != nil {
		return err
	}
	if !guarded {
		return write(ctx)
	}
	// Same-process pushes of one path queue here first. The advisory lock is
	// held by a transaction, so every waiter blocked inside it pins a pool
	// connection while the holder still needs one for its own statements;
	// queueing locally keeps that to one waiter per key per instance.
	unlock := localPathLocks.lock(blobKey)
	defer unlock()
	return d.Assets.WithBlobKeyLock(ctx, blobKey, func(ctx context.Context) error {
		// Re-read under the lock: the unlocked check above only spares an
		// obvious redeploy the lock round-trip.
		if err := assetAbsent(ctx, d.Assets, repo, filePath); err != nil {
			return err
		}
		return write(ctx)
	})
}

// keyedMutex is a set of mutexes created on demand and dropped once nobody
// holds or waits for them.
type keyedMutex struct {
	mu sync.Mutex
	m  map[string]*keyedEntry
}

type keyedEntry struct {
	mu   sync.Mutex
	refs int
}

var localPathLocks keyedMutex

func (k *keyedMutex) lock(key string) (unlock func()) {
	k.mu.Lock()
	if k.m == nil {
		k.m = make(map[string]*keyedEntry)
	}
	e := k.m[key]
	if e == nil {
		e = &keyedEntry{}
		k.m[key] = e
	}
	e.refs++
	k.mu.Unlock()

	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		k.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(k.m, key)
		}
		k.mu.Unlock()
	}
}
