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

// writeBlobEntries streams each referenced blob into the archive once
// (deduplicated by blob key); unreadable blobs are skipped.
func (s *BackupService) writeBlobEntries(ctx context.Context, tw *tar.Writer, assets []domain.Asset) error {
	seen := map[string]bool{}
	for _, a := range assets {
		if a.BlobKey == "" || seen[a.BlobKey] {
			continue
		}
		seen[a.BlobKey] = true
		rc, size, err := s.BlobStore.Get(ctx, a.BlobKey)
		if err != nil {
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
	return nil
}

// collectComponents pages through all components of one repository.
func (s *BackupService) collectComponents(ctx context.Context, repoName string) []domain.Component {
	var out []domain.Component
	for offset := 0; ; offset += 500 {
		page, err := s.Components.List(ctx, repoName, 500, offset)
		if err != nil {
			break
		}
		out = append(out, page.Items...)
		if len(page.Items) < 500 {
			break
		}
	}
	return out
}

// collectAssets pages through all assets of one repository.
func (s *BackupService) collectAssets(ctx context.Context, repoName string) []domain.Asset {
	var out []domain.Asset
	for offset := 0; ; offset += 500 {
		page, err := s.Assets.List(ctx, repoName, 500, offset)
		if err != nil {
			break
		}
		out = append(out, page.Items...)
		if len(page.Items) < 500 {
			break
		}
	}
	return out
}

// Export writes a gzip-compressed tar archive of all data + blobs to w.
// The archive contains JSON files for metadata and binary entries under blobs/.
func (s *BackupService) Export(ctx context.Context, w io.Writer) (retErr error) {
	aw, err := newArchiveWriter(w)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := aw.Close(); cerr != nil && retErr == nil {
			retErr = cerr
		}
	}()
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
		allComponents = append(allComponents, s.collectComponents(ctx, repo.Name)...)
	}
	if err := writeJSONEntry(tw, "components.json", allComponents); err != nil {
		return err
	}

	// Assets: iterate per repository; also stream blobs inline.
	var allAssets []domain.Asset
	for _, repo := range repos {
		allAssets = append(allAssets, s.collectAssets(ctx, repo.Name)...)
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
	defer func() {
		if cerr := aw.Close(); cerr != nil && retErr == nil {
			retErr = cerr
		}
	}()
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
	allComponents := s.collectComponents(ctx, repoName)
	if err := writeJSONEntry(tw, "components.json", allComponents); err != nil {
		return err
	}

	// Assets (paginated).
	allAssets := s.collectAssets(ctx, repoName)
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
