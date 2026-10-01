package service

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// archiveWriter pairs the gzip and tar writers for a backup archive so callers
// propagate Close errors (a truncated archive must not look successful).
type archiveWriter struct {
	gw *gzip.Writer
	tw *tar.Writer
}

func newArchiveWriter(w io.Writer) (*archiveWriter, error) {
	gw, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	return &archiveWriter{gw: gw, tw: tar.NewWriter(gw)}, nil
}

// Close flushes tar then gzip, joining both errors.
func (aw *archiveWriter) Close() error {
	return errors.Join(aw.tw.Close(), aw.gw.Close())
}

// closeArchive closes aw into *retErr. A Close failure means a truncated
// archive, so it replaces an IncompleteBackupError — which promises the
// archive itself is sound — and otherwise only fills an empty *retErr.
func closeArchive(aw *archiveWriter, retErr *error) {
	cerr := aw.Close()
	if cerr == nil {
		return
	}
	var incomplete *IncompleteBackupError
	if *retErr == nil || errors.As(*retErr, &incomplete) {
		*retErr = cerr
	}
}

// IncompleteBackupError reports blobs an export had to leave out. The archive
// itself is complete and valid — every metadata section and every other blob
// is in it — so a caller may keep it, but must not report it as a full backup.
type IncompleteBackupError struct {
	Missing int
	First   error // the first missing blob's failure, e.g. an S3 auth error
}

func (e *IncompleteBackupError) Error() string {
	return fmt.Sprintf("%d blob(s) could not be read and are missing from the archive (first: %v)", e.Missing, e.First)
}

// writeBlobEntries streams each referenced blob into the archive once
// (deduplicated by blob key). A blob it cannot read — its store does not
// resolve, or Get fails — is left out and counted rather than skipped
// silently: the returned *IncompleteBackupError says how many, so a backup
// that lost bytes cannot pass for a complete one (#490 review).
func (s *BackupService) writeBlobEntries(ctx context.Context, tw *tar.Writer, assets []domain.Asset) error {
	seen := map[string]bool{}
	stores := storeCache{}
	var missing *IncompleteBackupError
	skip := func(key string, err error) {
		if missing == nil {
			missing = &IncompleteBackupError{First: fmt.Errorf("blob %s: %w", key, err)}
		}
		missing.Missing++
	}
	for _, a := range assets {
		// A canceled run (e.g. a scheduled backup that lost its lock) is an
		// error, not a long list of blobs that "could not be read".
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if a.BlobKey == "" || seen[a.BlobKey] {
			continue
		}
		seen[a.BlobKey] = true
		store, err := s.resolveStore(ctx, stores, a.BlobStoreID)
		if err != nil {
			skip(a.BlobKey, err)
			continue
		}
		rc, size, err := store.Get(ctx, a.BlobKey)
		if err != nil {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			skip(a.BlobKey, err)
			continue
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:    "blobs/" + a.BlobKey,
			Size:    size,
			Mode:    0o644,
			ModTime: time.Now(),
		}); err != nil {
			_ = rc.Close()
			return err
		}
		if _, err := io.Copy(tw, rc); err != nil {
			_ = rc.Close()
			return fmt.Errorf("copy blob %s: %w", a.BlobKey, err)
		}
		_ = rc.Close()
	}
	if missing != nil {
		return missing
	}
	return nil
}

// collectComponents pages through all components of one repository. A page
// that fails to load is an error, not the end of the list: an archive cut
// short there would pass for a complete backup (#569).
func (s *BackupService) collectComponents(ctx context.Context, repoName string) ([]domain.Component, error) {
	var out []domain.Component
	for offset := 0; ; offset += 500 {
		page, err := s.Components.List(ctx, repoName, 500, offset)
		if err != nil {
			return nil, fmt.Errorf("list components of %s: %w", repoName, err)
		}
		out = append(out, page.Items...)
		if len(page.Items) < 500 {
			return out, nil
		}
	}
}

// collectAssets pages through all assets of one repository; a failed page is
// an error, as in collectComponents.
func (s *BackupService) collectAssets(ctx context.Context, repoName string) ([]domain.Asset, error) {
	var out []domain.Asset
	for offset := 0; ; offset += 500 {
		page, err := s.Assets.List(ctx, repoName, 500, offset)
		if err != nil {
			return nil, fmt.Errorf("list assets of %s: %w", repoName, err)
		}
		out = append(out, page.Items...)
		if len(page.Items) < 500 {
			return out, nil
		}
	}
}

// Export writes a gzip-compressed tar archive of all data + blobs to w.
// The archive contains JSON files for metadata and binary entries under blobs/.
func (s *BackupService) Export(ctx context.Context, w io.Writer) (retErr error) {
	aw, err := newArchiveWriter(w)
	if err != nil {
		return err
	}
	defer closeArchive(aw, &retErr)
	tw := aw.tw

	manifest := map[string]any{
		"version": "1",
		"created": time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeJSONEntry(tw, "manifest.json", manifest); err != nil {
		return err
	}

	blobStores, err := s.BlobStores.List(ctx)
	if err != nil {
		return fmt.Errorf("list blob stores: %w", err)
	}
	if err := writeJSONEntry(tw, "blob_stores.json", blobStores); err != nil {
		return err
	}

	repos, err := s.Repos.List(ctx, "", "")
	if err != nil {
		return fmt.Errorf("list repos: %w", err)
	}
	if err := writeJSONEntry(tw, "repositories.json", repos); err != nil {
		return err
	}

	rawUsers, err := s.Users.List(ctx, "")
	if err != nil {
		return fmt.Errorf("list users: %w", err)
	}
	exportedUsers := make([]backupUser, len(rawUsers))
	for i, u := range rawUsers {
		exportedUsers[i] = backupUser{User: u, PasswordHash: u.PasswordHash}
	}
	if err := writeJSONEntry(tw, "users.json", exportedUsers); err != nil {
		return err
	}

	roles, err := s.Roles.List(ctx)
	if err != nil {
		return fmt.Errorf("list roles: %w", err)
	}
	if err := writeJSONEntry(tw, "roles.json", roles); err != nil {
		return err
	}

	policies, err := s.Policies.List(ctx)
	if err != nil {
		return fmt.Errorf("list policies: %w", err)
	}
	if err := writeJSONEntry(tw, "cleanup_policies.json", policies); err != nil {
		return err
	}

	// Components: iterate per repository to stay within reasonable query sizes.
	var allComponents []domain.Component
	for _, repo := range repos {
		components, err := s.collectComponents(ctx, repo.Name)
		if err != nil {
			return err
		}
		allComponents = append(allComponents, components...)
	}
	if err := writeJSONEntry(tw, "components.json", allComponents); err != nil {
		return err
	}

	// Assets: iterate per repository; also stream blobs inline.
	var allAssets []domain.Asset
	for _, repo := range repos {
		assets, err := s.collectAssets(ctx, repo.Name)
		if err != nil {
			return err
		}
		allAssets = append(allAssets, assets...)
	}
	if err := writeJSONEntry(tw, "assets.json", allAssets); err != nil {
		return err
	}

	// Blobs: deduplicate by key.
	return s.writeBlobEntries(ctx, tw, allAssets)
}

// ExportRepo writes a gzip-compressed tar archive scoped to one repository.
// Archive contains: manifest.json, repository.json, components.json, assets.json, blobs/<key>.
// Returns ErrRepoNotFound if repoName does not exist.
func (s *BackupService) ExportRepo(ctx context.Context, repoName string, w io.Writer) (retErr error) {
	repo, err := s.Repos.Get(ctx, repoName)
	if errors.Is(err, repository.ErrNotFound) {
		return fmt.Errorf("%w: %s", ErrRepoNotFound, repoName)
	}
	if err != nil {
		return err
	}

	aw, err := newArchiveWriter(w)
	if err != nil {
		return err
	}
	defer closeArchive(aw, &retErr)
	tw := aw.tw

	manifest := map[string]any{
		"version":  "1",
		"created":  time.Now().UTC().Format(time.RFC3339),
		"repoName": repoName,
	}
	if err := writeJSONEntry(tw, "manifest.json", manifest); err != nil {
		return err
	}
	if err := writeJSONEntry(tw, "repository.json", *repo); err != nil {
		return err
	}

	// Components (paginated).
	allComponents, err := s.collectComponents(ctx, repoName)
	if err != nil {
		return err
	}
	if err := writeJSONEntry(tw, "components.json", allComponents); err != nil {
		return err
	}

	// Assets (paginated).
	allAssets, err := s.collectAssets(ctx, repoName)
	if err != nil {
		return err
	}
	if err := writeJSONEntry(tw, "assets.json", allAssets); err != nil {
		return err
	}

	// Blobs (deduplicated by key).
	return s.writeBlobEntries(ctx, tw, allAssets)
}

// writeJSONEntry serializes v as JSON and appends it as a tar entry named name.
func writeJSONEntry(tw *tar.Writer, name string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", name, err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Name:    name,
		Size:    int64(len(data)),
		Mode:    0o644,
		ModTime: time.Now(),
	}); err != nil {
		return err
	}
	_, err = tw.Write(data)
	return err
}
