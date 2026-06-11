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

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// backupArchive holds a backup tar.gz decoded in one pass: JSON sections by
// entry name, blob payloads by blob key.
type backupArchive struct {
	entries map[string][]byte
	blobs   map[string][]byte
}

func readBackupArchive(r io.Reader) (*backupArchive, error) {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("not a gzip archive: %w", err)
	}
	defer func() { _ = gr.Close() }()
	tr := tar.NewReader(gr)
	a := &backupArchive{entries: map[string][]byte{}, blobs: map[string][]byte{}}
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
		if strings.HasPrefix(hdr.Name, "blobs/") {
			a.blobs[strings.TrimPrefix(hdr.Name, "blobs/")] = data
		} else {
			a.entries[hdr.Name] = data
		}
	}
	return a, nil
}

// unmarshal decodes the named JSON section into v; absent sections are a no-op
// (matching the prior switch-based behavior of ignoring unmarshal errors).
func (a *backupArchive) unmarshal(name string, v any) {
	if data, ok := a.entries[name]; ok {
		_ = json.Unmarshal(data, v)
	}
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
func (s *BackupService) ImportRepo(ctx context.Context, r io.Reader, targetName, conflictMode string) (*ImportRepoStats, error) {
	if conflictMode == "" {
		conflictMode = "skip"
	}
	if conflictMode == "rename" && targetName == "" {
		return nil, fmt.Errorf("conflictMode=rename requires non-empty targetName")
	}

	arc, err := readBackupArchive(r)
	if err != nil {
		return nil, err
	}
	var archivedRepo domain.Repository
	var components []domain.Component
	var assets []domain.Asset
	arc.unmarshal("repository.json", &archivedRepo)
	arc.unmarshal("components.json", &components)
	arc.unmarshal("assets.json", &assets)
	blobs := arc.blobs

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

	compIDMap := s.importRepoComponents(ctx, components, destRepo, finalName, conflictMode, stats)
	s.importRepoAssets(ctx, assets, blobs, destRepo, finalName, conflictMode, blobStoreID, compIDMap, stats)

	return stats, nil
}

// importRepoComponents imports archived components into the destination
// repository, deduplicating against existing ones for skip/merge modes.
// Returns the archived-ID → new/existing-ID map used to re-link assets.
func (s *BackupService) importRepoComponents(ctx context.Context, components []domain.Component, destRepo *domain.Repository, finalName, conflictMode string, stats *ImportRepoStats) map[string]string {
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
	return compIDMap
}

// importRepoAssets imports archived assets (and their blob bytes) into the
// destination repository, deduplicating by path for skip/merge modes.
func (s *BackupService) importRepoAssets(ctx context.Context, assets []domain.Asset, blobs map[string][]byte, destRepo *domain.Repository, finalName, conflictMode, blobStoreID string, compIDMap map[string]string, stats *ImportRepoStats) {
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
}

// Restore reads a backup archive (as produced by Export) and re-creates all data.
// Existing records (matched by logical key: name, username, repo+path, etc.) are skipped.
// Returns stats on what was imported.
func (s *BackupService) Restore(ctx context.Context, r io.Reader) (*RestoreStats, error) {
	arc, err := readBackupArchive(r)
	if err != nil {
		return nil, err
	}
	var (
		blobStores []domain.BlobStore
		repos      []domain.Repository
		users      []backupUser
		roles      []domain.Role
		policies   []domain.CleanupPolicy
		components []domain.Component
		assets     []domain.Asset
	)
	arc.unmarshal("blob_stores.json", &blobStores)
	arc.unmarshal("repositories.json", &repos)
	arc.unmarshal("users.json", &users)
	arc.unmarshal("roles.json", &roles)
	arc.unmarshal("cleanup_policies.json", &policies)
	arc.unmarshal("components.json", &components)
	arc.unmarshal("assets.json", &assets)
	blobs := arc.blobs

	stats := &RestoreStats{}

	bsNameToID, oldBSIDToName := s.restoreBlobStores(ctx, blobStores, stats)
	repoNameToID := s.restoreRepos(ctx, repos, bsNameToID, oldBSIDToName, stats)
	s.restoreUsers(ctx, users, stats)
	s.restoreRoles(ctx, roles, stats)
	s.restorePolicies(ctx, policies, stats)
	compIDMap := s.restoreComponents(ctx, components, repoNameToID, stats)
	s.restoreAssets(ctx, assets, blobs, repoNameToID, compIDMap, bsNameToID, oldBSIDToName, stats)

	return stats, nil
}

// restoreBlobStores re-creates blob stores, skipping existing ones (by name).
// Returns name → new DB id and old archive UUID → name maps for asset FKs.
func (s *BackupService) restoreBlobStores(ctx context.Context, blobStores []domain.BlobStore, stats *RestoreStats) (bsNameToID, oldBSIDToName map[string]string) {
	bsNameToID = map[string]string{} // name → new DB id (for asset FK)
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
	// Build old-UUID → name map so asset BlobStore references can be remapped.
	oldBSIDToName = map[string]string{}
	for _, bs := range blobStores {
		oldBSIDToName[bs.ID] = bs.Name
	}
	return bsNameToID, oldBSIDToName
}

// restoreRepos re-creates repositories, skipping existing ones (by name) and
// remapping blob store references. Returns name → new DB id map.
func (s *BackupService) restoreRepos(ctx context.Context, repos []domain.Repository, bsNameToID, oldBSIDToName map[string]string, stats *RestoreStats) map[string]string {
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
	return repoNameToID
}

// restoreUsers re-creates users, skipping existing ones (by username).
func (s *BackupService) restoreUsers(ctx context.Context, users []backupUser, stats *RestoreStats) {
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
}

// restoreRoles re-creates roles, skipping existing ones (by ID).
func (s *BackupService) restoreRoles(ctx context.Context, roles []domain.Role, stats *RestoreStats) {
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
}

// restorePolicies re-creates cleanup policies, skipping existing ones (by ID).
func (s *BackupService) restorePolicies(ctx context.Context, policies []domain.CleanupPolicy, stats *RestoreStats) {
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
}

// restoreComponents re-creates components, mapping backup component IDs →
// newly assigned IDs (needed for asset FK).
func (s *BackupService) restoreComponents(ctx context.Context, components []domain.Component, repoNameToID map[string]string, stats *RestoreStats) map[string]string {
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
	return compIDMap
}

// restoreAssets re-creates assets and their blob bytes, remapping component,
// repository, and blob store references.
func (s *BackupService) restoreAssets(ctx context.Context, assets []domain.Asset, blobs map[string][]byte, repoNameToID, compIDMap, bsNameToID, oldBSIDToName map[string]string, stats *RestoreStats) {
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
}
