// Package service contains business logic for backup and restore of all repository data.
package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// BackupService exports and restores all repository data (metadata + blobs).
type BackupService struct {
	BlobStores repository.BlobStoreRepo
	Repos      repository.RepositoryRepo
	Users      repository.UserRepo
	Roles      repository.RoleRepo
	Policies   repository.CleanupPolicyRepo
	Components repository.ComponentRepo
	Assets     repository.AssetRepo
	BlobStore  storage.BlobStore
}

// Sentinel errors for per-repository operations.
var (
	ErrRepoNotFound = errors.New("repository not found")
	ErrRepoConflict = errors.New("repository already exists")
)

// RestoreStats reports what was restored.
type RestoreStats struct {
	BlobStores int `json:"blobStores"`
	Repos      int `json:"repositories"`
	Users      int `json:"users"`
	Roles      int `json:"roles"`
	Policies   int `json:"cleanupPolicies"`
	Components int `json:"components"`
	Assets     int `json:"assets"`
	Blobs      int `json:"blobs"`
}

// backupUser carries the password hash in backup archives (json:"-" hides it in normal API responses).
type backupUser struct {
	domain.User
	PasswordHash string `json:"passwordHash"`
}

// Export writes a gzip-compressed tar archive of all data + blobs to w.
// The archive contains JSON files for metadata and binary entries under blobs/.
//
//nolint:gocyclo // large sequential archive-export function; splitting would hurt readability
func (s *BackupService) Export(ctx context.Context, w io.Writer) (retErr error) {
	gw, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := gw.Close(); cerr != nil && retErr == nil {
			retErr = cerr
		}
	}()
	tw := tar.NewWriter(gw)
	defer func() {
		if cerr := tw.Close(); cerr != nil && retErr == nil {
			retErr = cerr
		}
	}()

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
		offset := 0
		for {
			page, err := s.Components.List(ctx, repo.Name, 500, offset)
			if err != nil {
				break
			}
			allComponents = append(allComponents, page.Items...)
			if len(page.Items) < 500 {
				break
			}
			offset += 500
		}
	}
	if err := writeJSONEntry(tw, "components.json", allComponents); err != nil {
		return err
	}

	// Assets: iterate per repository; also stream blobs inline.
	var allAssets []domain.Asset
	for _, repo := range repos {
		offset := 0
		for {
			page, err := s.Assets.List(ctx, repo.Name, 500, offset)
			if err != nil {
				break
			}
			allAssets = append(allAssets, page.Items...)
			if len(page.Items) < 500 {
				break
			}
			offset += 500
		}
	}
	if err := writeJSONEntry(tw, "assets.json", allAssets); err != nil {
		return err
	}

	// Blobs: deduplicate by key.
	seen := map[string]bool{}
	for _, a := range allAssets {
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

// ExportRepo writes a gzip-compressed tar archive scoped to one repository.
// Archive contains: manifest.json, repository.json, components.json, assets.json, blobs/<key>.
// Returns ErrRepoNotFound if repoName does not exist.
func (s *BackupService) ExportRepo(ctx context.Context, repoName string, w io.Writer) (retErr error) {
	repo, err := s.Repos.Get(ctx, repoName)
	if err != nil {
		return err
	}
	if repo == nil {
		return fmt.Errorf("%w: %s", ErrRepoNotFound, repoName)
	}

	gw, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := gw.Close(); cerr != nil && retErr == nil {
			retErr = cerr
		}
	}()
	tw := tar.NewWriter(gw)
	defer func() {
		if cerr := tw.Close(); cerr != nil && retErr == nil {
			retErr = cerr
		}
	}()

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
	var allComponents []domain.Component
	for offset := 0; ; offset += 500 {
		page, err := s.Components.List(ctx, repoName, 500, offset)
		if err != nil {
			break
		}
		allComponents = append(allComponents, page.Items...)
		if len(page.Items) < 500 {
			break
		}
	}
	if err := writeJSONEntry(tw, "components.json", allComponents); err != nil {
		return err
	}

	// Assets (paginated).
	var allAssets []domain.Asset
	for offset := 0; ; offset += 500 {
		page, err := s.Assets.List(ctx, repoName, 500, offset)
		if err != nil {
			break
		}
		allAssets = append(allAssets, page.Items...)
		if len(page.Items) < 500 {
			break
		}
	}
	if err := writeJSONEntry(tw, "assets.json", allAssets); err != nil {
		return err
	}

	// Blobs (deduplicated by key).
	seen := map[string]bool{}
	for _, a := range allAssets {
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

// ImportRepoStats reports what was imported.
type ImportRepoStats struct {
	Repository   string `json:"repository"`
	Components   int    `json:"components"`
	Assets       int    `json:"assets"`
	Blobs        int    `json:"blobs"`
	ConflictMode string `json:"conflictMode"`
}

// ImportRepo reads a per-repository archive (as produced by ExportRepo) and
// creates the repository, components, assets, and blobs in the current instance.
//
// targetName — if non-empty, override the repository name from the archive.
// conflictMode — "skip" (default) | "merge" | "rename":
//   - skip: if repo exists, add only absent components (by name+version+group) and assets (by path).
//   - merge: currently an alias for "skip".
//   - rename: targetName must be non-empty; returns ErrRepoConflict if targetName is taken.
//
//nolint:gocyclo // large sequential archive-import function; splitting would hurt readability
func (s *BackupService) ImportRepo(ctx context.Context, r io.Reader, targetName, conflictMode string) (*ImportRepoStats, error) {
	if conflictMode == "" {
		conflictMode = "skip"
	}
	if conflictMode == "rename" && targetName == "" {
		return nil, fmt.Errorf("conflictMode=rename requires non-empty targetName")
	}

	gr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("not a gzip archive: %w", err)
	}
	defer func() { _ = gr.Close() }()
	tr := tar.NewReader(gr)

	var archivedRepo domain.Repository
	var components []domain.Component
	var assets []domain.Asset
	blobs := map[string][]byte{}

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read entry %s: %w", hdr.Name, err)
		}
		switch hdr.Name {
		case "repository.json":
			_ = json.Unmarshal(data, &archivedRepo)
		case "components.json":
			_ = json.Unmarshal(data, &components)
		case "assets.json":
			_ = json.Unmarshal(data, &assets)
		default:
			if strings.HasPrefix(hdr.Name, "blobs/") {
				key := strings.TrimPrefix(hdr.Name, "blobs/")
				blobs[key] = data
			}
		}
	}

	if archivedRepo.Name == "" {
		return nil, fmt.Errorf("invalid archive: missing or empty repository.json")
	}

	finalName := archivedRepo.Name
	if targetName != "" {
		finalName = targetName
	}

	stats := &ImportRepoStats{ConflictMode: conflictMode, Repository: finalName}

	// Resolve or create destination repository.
	destRepo, _ := s.Repos.Get(ctx, finalName)
	if destRepo == nil {
		newRepo := archivedRepo
		newRepo.ID = ""
		newRepo.Name = finalName
		newRepo.BlobStoreID = nil
		if err := s.Repos.Create(ctx, &newRepo); err != nil {
			return nil, fmt.Errorf("create repository: %w", err)
		}
		destRepo, _ = s.Repos.Get(ctx, finalName)
	} else if conflictMode == "rename" {
		return nil, fmt.Errorf("%w: %q", ErrRepoConflict, finalName)
	}
	if destRepo == nil {
		return nil, fmt.Errorf("repository %q not available after creation", finalName)
	}

	// Pick blob store ID for imported assets.
	blobStoreID := ""
	if destRepo.BlobStoreID != nil {
		blobStoreID = *destRepo.BlobStoreID
	}
	if blobStoreID == "" {
		bss, _ := s.BlobStores.List(ctx)
		if len(bss) > 0 {
			blobStoreID = bss[0].ID
		}
	}

	// Build existing-components map (group+name+version → id) for skip/merge dedup.
	existingCompIDs := map[string]string{}
	if conflictMode == "skip" || conflictMode == "merge" {
		for offset := 0; ; offset += 500 {
			page, _ := s.Components.List(ctx, finalName, 500, offset)
			if page == nil || len(page.Items) == 0 {
				break
			}
			for _, c := range page.Items {
				k := c.Group + "\x00" + c.Name + "\x00" + c.Version
				existingCompIDs[k] = c.ID
			}
			if len(page.Items) < 500 {
				break
			}
		}
	}

	// Import components.
	compIDMap := map[string]string{} // archived ID → new/existing ID
	for i := range components {
		comp := &components[i]
		oldID := comp.ID
		k := comp.Group + "\x00" + comp.Name + "\x00" + comp.Version

		if id, found := existingCompIDs[k]; found {
			compIDMap[oldID] = id
			continue
		}

		comp.ID = ""
		comp.RepositoryID = destRepo.ID
		comp.Repository = finalName
		if err := s.Components.Create(ctx, comp); err != nil {
			continue
		}
		compIDMap[oldID] = comp.ID
		stats.Components++
	}

	// Import assets.
	for i := range assets {
		a := &assets[i]

		newCompID, ok := compIDMap[a.ComponentID]
		if !ok {
			continue
		}

		// Dedup by path for skip/merge.
		if conflictMode == "skip" || conflictMode == "merge" {
			if existing, _ := s.Assets.GetByPath(ctx, finalName, a.Path); existing != nil {
				continue
			}
		}

		// Restore blob bytes.
		if a.BlobKey != "" {
			if data, ok := blobs[a.BlobKey]; ok {
				_ = s.BlobStore.Put(ctx, a.BlobKey, bytes.NewReader(data), int64(len(data)))
			}
		}

		a.ID = ""
		a.ComponentID = newCompID
		a.RepositoryID = destRepo.ID
		a.Repository = finalName
		if blobStoreID != "" {
			a.BlobStoreID = blobStoreID
		}
		if err := s.Assets.Create(ctx, a); err != nil {
			continue
		}
		stats.Assets++
		if a.BlobKey != "" {
			if _, hadBlob := blobs[a.BlobKey]; hadBlob {
				stats.Blobs++
			}
		}
	}

	return stats, nil
}

// Restore reads a backup archive (as produced by Export) and re-creates all data.
// Existing records (matched by logical key: name, username, repo+path, etc.) are skipped.
// Returns stats on what was imported.
//
//nolint:gocyclo // large sequential archive-restore function; splitting would hurt readability
func (s *BackupService) Restore(ctx context.Context, r io.Reader) (*RestoreStats, error) {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("not a gzip archive: %w", err)
	}
	defer func() { _ = gr.Close() }()
	tr := tar.NewReader(gr)

	// First pass: read all JSON sections and blob data into memory.
	var (
		blobStores []domain.BlobStore
		repos      []domain.Repository
		users      []backupUser
		roles      []domain.Role
		policies   []domain.CleanupPolicy
		components []domain.Component
		assets     []domain.Asset
		blobs      = map[string][]byte{}
	)

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read entry %s: %w", hdr.Name, err)
		}
		switch hdr.Name {
		case "blob_stores.json":
			_ = json.Unmarshal(data, &blobStores)
		case "repositories.json":
			_ = json.Unmarshal(data, &repos)
		case "users.json":
			_ = json.Unmarshal(data, &users)
		case "roles.json":
			_ = json.Unmarshal(data, &roles)
		case "cleanup_policies.json":
			_ = json.Unmarshal(data, &policies)
		case "components.json":
			_ = json.Unmarshal(data, &components)
		case "assets.json":
			_ = json.Unmarshal(data, &assets)
		default:
			if strings.HasPrefix(hdr.Name, "blobs/") {
				key := strings.TrimPrefix(hdr.Name, "blobs/")
				blobs[key] = data
			}
		}
	}

	stats := &RestoreStats{}

	// ── BlobStores ──────────────────────────────────────────────
	bsNameToID := map[string]string{} // name → new DB id (for asset FK)
	for i := range blobStores {
		bs := &blobStores[i]
		existing, _ := s.BlobStores.Get(ctx, bs.Name)
		if existing != nil {
			bsNameToID[bs.Name] = existing.ID
			continue
		}
		bs.ID = "" // let DB assign
		if err := s.BlobStores.Create(ctx, bs); err != nil {
			continue
		}
		bsNameToID[bs.Name] = bs.ID
		stats.BlobStores++
	}
	// Build old-UUID → new-UUID map for BlobStore references in assets.
	oldBSIDToName := map[string]string{}
	for _, bs := range blobStores {
		oldBSIDToName[bs.ID] = bs.Name
	}

	// ── Repositories ────────────────────────────────────────────
	repoNameToID := map[string]string{}
	for i := range repos {
		repo := &repos[i]
		existing, _ := s.Repos.Get(ctx, repo.Name)
		if existing != nil {
			repoNameToID[repo.Name] = existing.ID
			continue
		}
		oldBSID := ""
		if repo.BlobStoreID != nil {
			oldBSID = *repo.BlobStoreID
		}
		repo.ID = ""
		if oldBSID != "" {
			if bsName, ok := oldBSIDToName[oldBSID]; ok {
				if newID, ok2 := bsNameToID[bsName]; ok2 {
					repo.BlobStoreID = &newID
				}
			}
		}
		if err := s.Repos.Create(ctx, repo); err != nil {
			continue
		}
		repoNameToID[repo.Name] = repo.ID
		stats.Repos++
	}

	// ── Users ───────────────────────────────────────────────────
	for i := range users {
		u := &users[i]
		existing, _ := s.Users.Get(ctx, u.Username)
		if existing != nil {
			continue
		}
		domUser := u.User
		domUser.PasswordHash = u.PasswordHash
		domUser.ID = ""
		if err := s.Users.Create(ctx, &domUser); err != nil {
			continue
		}
		stats.Users++
	}

	// ── Roles ───────────────────────────────────────────────────
	for i := range roles {
		role := &roles[i]
		existing, _ := s.Roles.Get(ctx, role.ID)
		if existing != nil {
			continue
		}
		role.ID = ""
		if err := s.Roles.Create(ctx, role); err != nil {
			continue
		}
		stats.Roles++
	}

	// ── Cleanup Policies ────────────────────────────────────────
	for i := range policies {
		p := &policies[i]
		existing, _ := s.Policies.Get(ctx, p.ID)
		if existing != nil {
			continue
		}
		p.ID = ""
		if err := s.Policies.Create(ctx, p); err != nil {
			continue
		}
		stats.Policies++
	}

	// ── Components ──────────────────────────────────────────────
	// Map backup component IDs → newly assigned IDs (needed for asset FK).
	compIDMap := map[string]string{}
	for i := range components {
		comp := &components[i]
		oldID := comp.ID
		repoID, ok := repoNameToID[comp.Repository]
		if !ok {
			continue
		}
		comp.RepositoryID = repoID
		comp.ID = ""
		if err := s.Components.Create(ctx, comp); err != nil {
			continue
		}
		compIDMap[oldID] = comp.ID
		stats.Components++
	}

	// ── Assets + Blobs ──────────────────────────────────────────
	for i := range assets {
		a := &assets[i]

		repoID, ok := repoNameToID[a.Repository]
		if !ok {
			continue
		}

		newCompID, ok := compIDMap[a.ComponentID]
		if !ok {
			continue
		}

		// Map BlobStore ID.
		newBSID := ""
		if bsName, ok := oldBSIDToName[a.BlobStoreID]; ok {
			newBSID = bsNameToID[bsName]
		}
		if newBSID == "" {
			// Fallback: pick the first available blob store.
			for _, id := range bsNameToID {
				newBSID = id
				break
			}
		}

		// Restore blob bytes.
		if a.BlobKey != "" {
			if data, ok := blobs[a.BlobKey]; ok {
				_ = s.BlobStore.Put(ctx, a.BlobKey, bytes.NewReader(data), int64(len(data)))
				stats.Blobs++
			}
		}

		a.ComponentID = newCompID
		a.RepositoryID = repoID
		a.BlobStoreID = newBSID
		a.ID = ""
		if err := s.Assets.Create(ctx, a); err != nil {
			continue
		}
		stats.Assets++
	}

	return stats, nil
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
