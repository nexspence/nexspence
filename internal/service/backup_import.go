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
	"sort"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
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
	Repository string `json:"repository"`
	Components int    `json:"components"`
	Assets     int    `json:"assets"`
	Blobs      int    `json:"blobs"`
	// BlobsFailed counts blobs that could not be written; their assets are
	// not imported, so re-running the import retries them.
	BlobsFailed  int    `json:"blobsFailed"`
	ConflictMode string `json:"conflictMode"`
	FailureReport
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
		// The archive is input like any create request (#619).
		if err := validateRepoIdentity(&newRepo); err != nil {
			return nil, err
		}
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

	// Imported assets go where the repository's own uploads go: its store, or
	// the installation default when it has none — never simply the first
	// store by name (#549).
	blobStoreID := ""
	if destRepo.BlobStoreID != nil {
		blobStoreID = strings.TrimSpace(*destRepo.BlobStoreID)
	}
	if blobStoreID == "" {
		def, err := repository.DefaultBlobStore(ctx, s.BlobStores)
		if err != nil {
			return nil, fmt.Errorf("destination blob store for %q: %w", finalName, err)
		}
		blobStoreID = def.ID
	}
	// A repository on a group store records, per asset, the physical member
	// that holds the bytes — never the group, which has none of its own.
	blobStoreID, err = s.physicalStoreID(ctx, blobStoreID)
	if err != nil {
		return nil, err
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
			s.recordFailure(&stats.FailureReport, "import", "component", finalName+"/"+componentLabel(comp), err)
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
	stores := storeCache{}
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

		// Restore blob bytes to the asset's actual destination store. A blob
		// that cannot be written leaves its asset out (see putArchivedBlob).
		if a.BlobKey != "" && arc.hasBlob(a.BlobKey) {
			if err := s.putArchivedBlob(ctx, stores, arc, a.BlobKey, blobStoreID); err != nil {
				stats.BlobsFailed++
				s.recordFailure(&stats.FailureReport, "import", "asset", finalName+a.Path, fmt.Errorf("write blob %s: %w", a.BlobKey, err))
				continue
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
			s.recordFailure(&stats.FailureReport, "import", "asset", finalName+a.Path, err)
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
	// Old-UUID → name, so asset/repo BlobStore references can be remapped.
	// Built before the loop below: Create overwrites bs.ID with the new
	// DB-assigned id, so reading it afterwards would key every store that had
	// to be re-created by its NEW id, and no archived reference would match.
	oldBSIDToName = make(map[string]string, len(blobStores))
	for _, bs := range blobStores {
		oldBSIDToName[bs.ID] = bs.Name
	}
	// A group names its members by id, so every physical store has to exist —
	// with its id on this instance — before a group that references it.
	sort.SliceStable(blobStores, func(i, j int) bool {
		return blobStores[i].Type != "group" && blobStores[j].Type == "group"
	})
	for i := range blobStores {
		bs := &blobStores[i]
		existing, _ := s.BlobStores.Get(ctx, bs.Name)
		if existing != nil {
			bsNameToID[bs.Name] = existing.ID
			continue
		}
		if bs.Type == "group" {
			members := remapGroupMembers(bs.Config["member_ids"], oldBSIDToName, bsNameToID)
			if len(members) == 0 {
				// An empty group is not a valid store.
				s.recordFailure(&stats.FailureReport, "restore", "blobStore", bs.Name, errors.New("none of its member stores exist or could be restored"))
				continue
			}
			cfg := make(map[string]any, len(bs.Config))
			for k, v := range bs.Config {
				cfg[k] = v
			}
			cfg["member_ids"] = members
			bs.Config = cfg
		}
		bs.ID = "" // let DB assign
		if err := s.BlobStores.Create(ctx, bs); err != nil {
			s.recordFailure(&stats.FailureReport, "restore", "blobStore", bs.Name, err)
			continue
		}
		bsNameToID[bs.Name] = bs.ID
		stats.BlobStores++
	}
	return bsNameToID, oldBSIDToName
}

// putArchivedBlob streams the archive's spooled payload for key into the
// store blobStoreID resolves to. The caller skips the asset on an error: a row
// pointing at bytes that were never written would serve broken downloads.
// ImportRepo dedups by path, so re-running the import fills the gap.
func (s *BackupService) putArchivedBlob(ctx context.Context, stores storeCache, arc *backupArchive, key, blobStoreID string) error {
	store, err := s.resolveStore(ctx, stores, blobStoreID)
	if err != nil {
		return err
	}
	rc, size, ok := arc.openBlob(key)
	if !ok {
		return fmt.Errorf("blob %s: spooled payload unreadable", key)
	}
	defer func() { _ = rc.Close() }()
	return store.Put(ctx, key, rc, size)
}

// physicalStoreID maps a group blob store to the member an import writes to:
// the first member with capacity, the write_to_first_fill order uploads use.
// Round-robin is not reproduced — that needs the upload path's per-process
// counters, and one import is a single batch anyway. Any other store's id is
// returned unchanged.
func (s *BackupService) physicalStoreID(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	bs, err := s.BlobStores.GetByID(ctx, id)
	if err != nil {
		return "", fmt.Errorf("destination blob store %s: %w", id, err)
	}
	if bs == nil || bs.Type != "group" {
		return id, nil
	}
	for _, mid := range groupMemberIDs(bs.Config["member_ids"]) {
		m, err := s.BlobStores.GetByID(ctx, mid)
		if err != nil || m == nil || m.Type == "group" {
			continue
		}
		if m.QuotaBytes == nil || m.UsedBytes < *m.QuotaBytes {
			return m.ID, nil
		}
	}
	return "", fmt.Errorf("group blob store %q has no member that can take the import", bs.Name)
}

// groupMemberIDs reads a group's member_ids config value as decoded from JSON
// ([]any) or as set from Go ([]string).
func groupMemberIDs(raw any) []string {
	var ids []string
	switch v := raw.(type) {
	case []string:
		ids = v
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				ids = append(ids, s)
			}
		}
	}
	return ids
}

// remapGroupMembers translates a group's archived member ids to this
// instance's ids (old id → name → id here), dropping members that do not
// exist here. raw is the member_ids config value as decoded from JSON.
func remapGroupMembers(raw any, oldBSIDToName, bsNameToID map[string]string) []string {
	ids := groupMemberIDs(raw)
	out := make([]string, 0, len(ids))
	for _, old := range ids {
		if newID, ok := bsNameToID[oldBSIDToName[old]]; ok {
			out = append(out, newID)
		}
	}
	return out
}

// fallbackBlobStoreID picks the store for an asset whose archived store could
// not be mapped: "default" — where restoreRepos sends a repo in the same
// situation — or, without one, the first store by name. Deterministic, unlike
// ranging over the map, which could scatter one restore's assets across
// arbitrary stores.
func fallbackBlobStoreID(bsNameToID map[string]string) string {
	if id, ok := bsNameToID["default"]; ok {
		return id
	}
	names := make([]string, 0, len(bsNameToID))
	for name := range bsNameToID {
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return bsNameToID[names[0]]
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
			// An archived store id means nothing on this instance: remap it by
			// name, or — when that store could not be restored — drop it so
			// the repo falls back to the default store, instead of keeping an
			// id whose FK makes Create fail and silently drops the whole repo.
			repo.BlobStoreID = nil
			if bsName, ok := oldBSIDToName[oldBSID]; ok {
				if newID, ok2 := bsNameToID[bsName]; ok2 {
					repo.BlobStoreID = &newID
				}
			}
		}
		if err := validateRepoIdentity(repo); err != nil {
			s.recordFailure(&stats.FailureReport, "restore", "repository", repo.Name, err)
			continue
		}
		if err := s.Repos.Create(ctx, repo); err != nil {
			s.recordFailure(&stats.FailureReport, "restore", "repository", repo.Name, err)
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
			s.recordFailure(&stats.FailureReport, "restore", "user", u.Username, err)
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
			s.recordFailure(&stats.FailureReport, "restore", "role", role.Name, err)
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
			s.recordFailure(&stats.FailureReport, "restore", "cleanupPolicy", p.Name, err)
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
			s.recordFailure(&stats.FailureReport, "restore", "component", comp.Repository+"/"+componentLabel(comp), err)
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
	stores := storeCache{}
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
			newBSID = fallbackBlobStoreID(bsNameToID)
		}

		// Restore blob bytes to the asset's actual destination store. A blob
		// that cannot be written leaves its asset out (see putArchivedBlob).
		if a.BlobKey != "" && arc.hasBlob(a.BlobKey) {
			if err := s.putArchivedBlob(ctx, stores, arc, a.BlobKey, newBSID); err != nil {
				stats.BlobsFailed++
				s.recordFailure(&stats.FailureReport, "restore", "asset", a.Repository+a.Path, fmt.Errorf("write blob %s: %w", a.BlobKey, err))
				continue
			}
			stats.Blobs++
		}

		a.ComponentID = newCompID
		a.RepositoryID = repoID
		a.BlobStoreID = newBSID
		a.ID = ""
		if err := s.Assets.Create(ctx, a); err != nil {
			s.recordFailure(&stats.FailureReport, "restore", "asset", a.Repository+a.Path, err)
			continue
		}
		stats.Assets++
	}
}
