package service

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// backupArchive holds a backup tar.gz decoded in one pass. JSON sections stay
// in memory — they are small and are unmarshalled immediately — while blob
// payloads are spooled to a temporary directory. Holding those in memory meant
// an 8 GiB archive cost 8 GiB of process heap.
//
// The caller owns the spool: Close removes it, and every path that builds an
// archive must call it.
type backupArchive struct {
	entries  map[string][]byte
	blobDir  string
	blobFile map[string]string // blob key → spooled file name
	blobSize map[string]int64
}

// defaultMaxImportBytes caps total decompressed bytes read from a backup
// archive, guarding against gzip bombs that would otherwise fill the disk.
const defaultMaxImportBytes = 8 << 30 // 8 GiB

// maxImportEntries caps how many members an archive may contain. The byte limit
// alone does not bound work: millions of one-byte entries cost CPU and
// allocation without ever approaching it.
const maxImportEntries = 5_000_000

func readBackupArchive(r io.Reader) (*backupArchive, error) {
	return readBackupArchiveLimited(r, defaultMaxImportBytes)
}

// readBackupArchiveLimited decodes a backup tar.gz, returning an error once the
// cumulative decompressed size of all entries exceeds maxBytes.
func readBackupArchiveLimited(r io.Reader, maxBytes int64) (a *backupArchive, err error) {
	return readBackupArchiveWithLimits(r, maxBytes, maxImportEntries)
}

// readBackupArchiveWithLimits is readBackupArchiveLimited with the entry cap
// exposed, so tests can exercise it without building a five-million-entry tar.
func readBackupArchiveWithLimits(r io.Reader, maxBytes int64, maxEntries int) (a *backupArchive, err error) {
	gr, gerr := gzip.NewReader(r)
	if gerr != nil {
		return nil, fmt.Errorf("not a gzip archive: %w", gerr)
	}
	defer func() { _ = gr.Close() }()

	dir, derr := os.MkdirTemp("", "nexspence-import-*")
	if derr != nil {
		return nil, fmt.Errorf("create import spool: %w", derr)
	}
	a = &backupArchive{
		entries:  map[string][]byte{},
		blobDir:  dir,
		blobFile: map[string]string{},
		blobSize: map[string]int64{},
	}
	// Any failure below leaves nothing on disk.
	defer func() {
		if err != nil {
			_ = a.Close()
			a = nil
		}
	}()

	tr := tar.NewReader(gr)
	var total int64
	var count int
	for {
		hdr, nerr := tr.Next()
		if errors.Is(nerr, io.EOF) {
			break
		}
		if nerr != nil {
			return a, fmt.Errorf("read archive: %w", nerr)
		}
		count++
		if count > maxEntries {
			return a, fmt.Errorf("backup archive exceeds %d entries", maxEntries)
		}
		remaining := maxBytes - total
		if remaining <= 0 {
			return a, fmt.Errorf("backup archive exceeds %d byte decompression limit", maxBytes)
		}

		if key, ok := strings.CutPrefix(hdr.Name, "blobs/"); ok {
			n, werr := a.spoolBlob(key, tr, remaining)
			if werr != nil {
				return a, werr
			}
			total += n
			continue
		}

		// Read one extra byte: if the entry fills remaining+1, it overflowed the cap.
		data, rerr := io.ReadAll(io.LimitReader(tr, remaining+1))
		if rerr != nil {
			return a, fmt.Errorf("read entry %s: %w", hdr.Name, rerr)
		}
		if int64(len(data)) > remaining {
			return a, fmt.Errorf("backup archive exceeds %d byte decompression limit", maxBytes)
		}
		total += int64(len(data))
		a.entries[hdr.Name] = data
	}
	return a, nil
}

// spoolBlob copies one blob payload to the spool directory, refusing to write
// more than remaining bytes. Returns how much was written.
func (a *backupArchive) spoolBlob(key string, r io.Reader, remaining int64) (int64, error) {
	name := fmt.Sprintf("blob-%d", len(a.blobFile))
	f, err := os.Create(filepath.Join(a.blobDir, name)) //nolint:gosec // name is generated, not caller-controlled
	if err != nil {
		return 0, fmt.Errorf("spool blob %s: %w", key, err)
	}
	defer func() { _ = f.Close() }()

	// One byte past the budget tells us it overflowed rather than exactly fit.
	n, err := io.Copy(f, io.LimitReader(r, remaining+1))
	if err != nil {
		return 0, fmt.Errorf("spool blob %s: %w", key, err)
	}
	if n > remaining {
		return 0, fmt.Errorf("backup archive exceeds decompression limit")
	}
	a.blobFile[key] = name
	a.blobSize[key] = n
	return n, nil
}

// Close removes the spool directory. Safe to call more than once.
func (a *backupArchive) Close() error {
	if a == nil || a.blobDir == "" {
		return nil
	}
	dir := a.blobDir
	a.blobDir = ""
	return os.RemoveAll(dir)
}

// hasBlob reports whether the archive carried a payload for key.
func (a *backupArchive) hasBlob(key string) bool {
	_, ok := a.blobFile[key]
	return ok
}

// blobPath returns the spooled file for key.
func (a *backupArchive) blobPath(key string) (string, bool) {
	name, ok := a.blobFile[key]
	if !ok {
		return "", false
	}
	return filepath.Join(a.blobDir, name), true
}

// openBlob returns a reader over the spooled payload and its size. The caller
// closes the reader.
func (a *backupArchive) openBlob(key string) (io.ReadCloser, int64, bool) {
	path, ok := a.blobPath(key)
	if !ok {
		return nil, 0, false
	}
	f, err := os.Open(path) //nolint:gosec // path is inside our own spool directory
	if err != nil {
		return nil, 0, false
	}
	return f, a.blobSize[key], true
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
	// The spool holds every blob payload in the archive; release it as soon as
	// the import is done, however it ends.
	defer func() { _ = arc.Close() }()
	var archivedRepo domain.Repository
	var components []domain.Component
	var assets []domain.Asset
	arc.unmarshal("repository.json", &archivedRepo)
	arc.unmarshal("components.json", &components)
	arc.unmarshal("assets.json", &assets)

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
	s.importRepoAssets(ctx, assets, arc, destRepo, finalName, conflictMode, blobStoreID, compIDMap, stats)

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
func (s *BackupService) importRepoAssets(ctx context.Context, assets []domain.Asset, arc *backupArchive, destRepo *domain.Repository, finalName, conflictMode, blobStoreID string, compIDMap map[string]string, stats *ImportRepoStats) {
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

		// Restore blob bytes, streamed from the spool rather than held in memory.
		if a.BlobKey != "" {
			if rc, size, ok := arc.openBlob(a.BlobKey); ok {
				_ = s.BlobStore.Put(ctx, a.BlobKey, rc, size)
				_ = rc.Close()
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
			if arc.hasBlob(a.BlobKey) {
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
	// The spool holds every blob payload in the archive; release it as soon as
	// the import is done, however it ends.
	defer func() { _ = arc.Close() }()
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

	stats := &RestoreStats{}

	bsNameToID, oldBSIDToName := s.restoreBlobStores(ctx, blobStores, stats)
	repoNameToID := s.restoreRepos(ctx, repos, bsNameToID, oldBSIDToName, stats)
	s.restoreUsers(ctx, users, stats)
	s.restoreRoles(ctx, roles, stats)
	s.restorePolicies(ctx, policies, stats)
	compIDMap := s.restoreComponents(ctx, components, repoNameToID, stats)
	s.restoreAssets(ctx, assets, arc, repoNameToID, compIDMap, bsNameToID, oldBSIDToName, stats)

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
func (s *BackupService) restoreAssets(ctx context.Context, assets []domain.Asset, arc *backupArchive, repoNameToID, compIDMap, bsNameToID, oldBSIDToName map[string]string, stats *RestoreStats) {
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

		// Restore blob bytes, streamed from the spool rather than held in memory.
		if a.BlobKey != "" {
			if rc, size, ok := arc.openBlob(a.BlobKey); ok {
				_ = s.BlobStore.Put(ctx, a.BlobKey, rc, size)
				_ = rc.Close()
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
