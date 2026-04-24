// Package testutil provides shared test helpers and in-memory mock implementations
// of repository and storage interfaces. It must only be imported from _test.go files.
package testutil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// ── compile-time interface assertions ────────────────────────
var (
	_ repository.RepositoryRepo      = (*RepoRepo)(nil)
	_ repository.BlobStoreRepo       = (*BlobStoreRepo)(nil)
	_ repository.ComponentRepo       = (*ComponentRepo)(nil)
	_ repository.AssetRepo           = (*AssetRepo)(nil)
	_ repository.CleanupPolicyRepo   = (*CleanupPolicyRepo)(nil)
	_ repository.AuditRepo           = (*AuditRepo)(nil)
	_ repository.UserRepo            = (*UserRepo)(nil)
	_ repository.RoleRepo            = (*RoleRepo)(nil)
	_ repository.ContentSelectorRepo = (*ContentSelectorRepo)(nil)
	_ repository.UserTokenRepo       = (*UserTokenRepo)(nil)
	_ repository.WebhookRepo         = (*WebhookRepo)(nil)
	_ repository.RoutingRuleRepo     = (*RoutingRuleRepo)(nil)
	_ repository.PrivilegeRepo       = (*PrivilegeRepo)(nil)
	_ storage.BlobStore              = (*BlobStore)(nil)
)

// ── RepositoryRepo ────────────────────────────────────────────

type RepoRepo struct {
	mu    sync.Mutex
	repos map[string]*domain.Repository
}

func NewRepoRepo(repos ...*domain.Repository) *RepoRepo {
	r := &RepoRepo{repos: make(map[string]*domain.Repository)}
	for _, repo := range repos {
		r.repos[repo.Name] = repo
	}
	return r
}

func (r *RepoRepo) List(_ context.Context, _, _ string) ([]domain.Repository, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.Repository, 0, len(r.repos))
	for _, v := range r.repos {
		out = append(out, *v)
	}
	return out, nil
}
func (r *RepoRepo) Get(_ context.Context, name string) (*domain.Repository, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.repos[name], nil
}
func (r *RepoRepo) GetByID(_ context.Context, id string) (*domain.Repository, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.repos {
		if v.ID == id {
			return v, nil
		}
	}
	return nil, nil
}
func (r *RepoRepo) Create(_ context.Context, repo *domain.Repository) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.repos[repo.Name] = repo
	return nil
}
func (r *RepoRepo) Update(_ context.Context, repo *domain.Repository) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.repos[repo.Name] = repo
	return nil
}
func (r *RepoRepo) Delete(_ context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.repos, name)
	return nil
}

func (r *RepoRepo) ListNamesByCleanupPolicyID(_ context.Context, policyID string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var names []string
	for _, v := range r.repos {
		for _, id := range v.CleanupPolicyIDs {
			if id == policyID {
				names = append(names, v.Name)
				break
			}
		}
	}
	return names, nil
}

func (r *RepoRepo) ListByBlobStoreID(_ context.Context, blobStoreID string) ([]domain.Repository, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Repository
	for _, v := range r.repos {
		if v.BlobStoreID != nil && *v.BlobStoreID == blobStoreID {
			out = append(out, *v)
		}
	}
	return out, nil
}

func (r *RepoRepo) HasAnyAnonymousDocker(_ context.Context) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.repos {
		if string(v.Format) == "docker" && v.AllowAnonymous {
			return true, nil
		}
	}
	return false, nil
}

func (r *RepoRepo) DetachCleanupPolicyID(_ context.Context, policyID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.repos {
		var next []string
		for _, id := range v.CleanupPolicyIDs {
			if id != policyID {
				next = append(next, id)
			}
		}
		v.CleanupPolicyIDs = next
	}
	return nil
}

// ── BlobStoreRepo ─────────────────────────────────────────────

type BlobStoreRepo struct {
	mu     sync.Mutex
	stores map[string]*domain.BlobStore
}

func NewBlobStoreRepo(stores ...*domain.BlobStore) *BlobStoreRepo {
	b := &BlobStoreRepo{stores: make(map[string]*domain.BlobStore)}
	for _, s := range stores {
		b.stores[s.Name] = s
	}
	if len(b.stores) == 0 {
		b.stores["default"] = &domain.BlobStore{
			ID:   "00000000-0000-0000-0000-000000000001",
			Name: "default",
			Type: "local",
		}
	}
	return b
}

func (b *BlobStoreRepo) List(_ context.Context) ([]domain.BlobStore, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]domain.BlobStore, 0, len(b.stores))
	for _, v := range b.stores {
		out = append(out, *v)
	}
	return out, nil
}
func (b *BlobStoreRepo) Get(_ context.Context, name string) (*domain.BlobStore, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stores[name], nil
}

func (b *BlobStoreRepo) GetByID(_ context.Context, id string) (*domain.BlobStore, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.stores {
		if s.ID == id {
			cp := *s
			return &cp, nil
		}
	}
	return nil, nil
}

func (b *BlobStoreRepo) Create(_ context.Context, s *domain.BlobStore) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stores[s.Name] = s
	return nil
}
func (b *BlobStoreRepo) Update(_ context.Context, s *domain.BlobStore) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stores[s.Name] = s
	return nil
}
func (b *BlobStoreRepo) Delete(_ context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.stores, name)
	return nil
}
func (b *BlobStoreRepo) UpdateUsedBytes(_ context.Context, name string, delta int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.stores[name]; ok {
		s.UsedBytes += delta
	}
	return nil
}

// ── ComponentRepo ─────────────────────────────────────────────

type ComponentRepo struct {
	mu         sync.Mutex
	components map[string]*domain.Component
	nextID     int
}

func NewComponentRepo() *ComponentRepo {
	return &ComponentRepo{components: make(map[string]*domain.Component)}
}

func (c *ComponentRepo) List(ctx context.Context, repoName string, limit, offset int) (*domain.Page[domain.Component], error) {
	return c.ListByRepoNames(ctx, []string{repoName}, limit, offset)
}

func (c *ComponentRepo) ListByRepoNames(_ context.Context, _ []string, _, _ int) (*domain.Page[domain.Component], error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	items := make([]domain.Component, 0, len(c.components))
	for _, v := range c.components {
		items = append(items, *v)
	}
	return &domain.Page[domain.Component]{Items: items}, nil
}
func (c *ComponentRepo) Get(_ context.Context, id string) (*domain.Component, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.components[id], nil
}
func (c *ComponentRepo) Search(_ context.Context, params domain.SearchParams) (*domain.Page[domain.Component], error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	items := make([]domain.Component, 0, len(c.components))
	for _, v := range c.components {
		if params.Repository == "" || v.Repository == params.Repository {
			items = append(items, *v)
		}
	}
	return &domain.Page[domain.Component]{Items: items}, nil
}

func (c *ComponentRepo) ListDockerBrowseRows(_ context.Context, _ []string, _ int) ([]domain.DockerBrowseRow, error) {
	return nil, nil
}

func (c *ComponentRepo) Create(_ context.Context, comp *domain.Component) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	comp.ID = fmt.Sprintf("comp-%d", c.nextID)
	c.components[comp.ID] = comp
	return nil
}
func (c *ComponentRepo) Delete(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.components, id)
	return nil
}

func (c *ComponentRepo) DeleteOrphans(_ context.Context, _ string) error {
	return nil
}

func (c *ComponentRepo) UpdateExtra(_ context.Context, id string, extra map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	comp := c.components[id]
	if comp == nil {
		return nil
	}
	if comp.Extra == nil {
		comp.Extra = make(map[string]any)
	}
	for k, v := range extra {
		comp.Extra[k] = v
	}
	return nil
}

// ── AssetRepo ─────────────────────────────────────────────────

type AssetRepo struct {
	mu     sync.Mutex
	assets map[string]*domain.Asset // key: "repo:path"
	byID   map[string]*domain.Asset
	nextID int
	Stale  []domain.Asset // populated by tests to control ListStale output
}

func NewAssetRepo() *AssetRepo {
	return &AssetRepo{
		assets: make(map[string]*domain.Asset),
		byID:   make(map[string]*domain.Asset),
	}
}

func (a *AssetRepo) List(_ context.Context, _ string, _, _ int) (*domain.Page[domain.Asset], error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	items := make([]domain.Asset, 0, len(a.byID))
	for _, v := range a.byID {
		items = append(items, *v)
	}
	return &domain.Page[domain.Asset]{Items: items}, nil
}
func (a *AssetRepo) Get(_ context.Context, id string) (*domain.Asset, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.byID[id], nil
}
func (a *AssetRepo) GetByPath(_ context.Context, repoName, path string) (*domain.Asset, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.assets[repoName+":"+path], nil
}
func (a *AssetRepo) SearchAssets(_ context.Context, _ domain.SearchParams) (*domain.Page[domain.Asset], error) {
	return &domain.Page[domain.Asset]{Items: []domain.Asset{}}, nil
}
func (a *AssetRepo) ListStale(_ context.Context, _ string, _ []string, _, _ int, _, _ string, limit int) ([]domain.Asset, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.Stale) == 0 {
		return nil, nil
	}
	n := limit
	if n <= 0 {
		n = 500
	}
	if len(a.Stale) > n {
		batch := append([]domain.Asset(nil), a.Stale[:n]...)
		a.Stale = a.Stale[n:]
		return batch, nil
	}
	out := append([]domain.Asset(nil), a.Stale...)
	a.Stale = nil
	return out, nil
}
func (a *AssetRepo) Create(_ context.Context, asset *domain.Asset) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nextID++
	asset.ID = fmt.Sprintf("asset-%d", a.nextID)
	// Index by repo name (for GetByPath) — matches what postgres impl does via JOIN
	key := asset.Repository + ":" + asset.Path
	a.assets[key] = asset
	a.byID[asset.ID] = asset
	return nil
}
func (a *AssetRepo) Delete(_ context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	asset, ok := a.byID[id]
	if !ok {
		return nil
	}
	delete(a.byID, id)
	delete(a.assets, asset.RepositoryID+":"+asset.Path)
	return nil
}
func (a *AssetRepo) IncrementDownload(_ context.Context, _ string) error { return nil }

func (a *AssetRepo) ListByComponentID(_ context.Context, componentID string) ([]domain.Asset, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []domain.Asset
	for _, v := range a.byID {
		if v.ComponentID == componentID {
			cp := *v
			out = append(out, cp)
		}
	}
	return out, nil
}

func (a *AssetRepo) ListAllBlobKeys(_ context.Context) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	seen := make(map[string]struct{})
	for _, v := range a.byID {
		if v.BlobKey != "" {
			seen[v.BlobKey] = struct{}{}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	return keys, nil
}

func (a *AssetRepo) SumSizeByRepo(_ context.Context, repoName string) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var total int64
	for _, v := range a.byID {
		if v.Repository == repoName {
			total += v.SizeBytes
		}
	}
	return total, nil
}

func (a *AssetRepo) ListPathsByRepo(_ context.Context, repoName, q string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	seen := make(map[string]struct{})
	for _, asset := range a.byID {
		if asset.Repository != repoName {
			continue
		}
		// extract all directory prefixes from path
		p := asset.Path
		for {
			idx := strings.LastIndex(p, "/")
			if idx <= 0 {
				break
			}
			p = p[:idx+1]
			if q == "" || strings.Contains(strings.ToLower(p), strings.ToLower(q)) {
				seen[p] = struct{}{}
			}
			p = p[:idx]
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (a *AssetRepo) ListByRepoAndPath(_ context.Context, _, _ string) ([]domain.Asset, error) {
	return nil, nil
}

func (a *AssetRepo) ListRawBrowseAssets(_ context.Context, _ []string) ([]domain.RawBrowseAsset, error) {
	return nil, nil
}

func (a *AssetRepo) CountByBlobKey(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

func (a *AssetRepo) ListRawAssetPaths(_ context.Context, repoName string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	seen := make(map[string]struct{})
	for _, asset := range a.byID {
		if asset.Repository == repoName {
			seen[asset.Path] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// ── CleanupPolicyRepo ─────────────────────────────────────────

type CleanupPolicyRepo struct {
	mu       sync.Mutex
	policies map[string]*domain.CleanupPolicy
	nextID   int
	Updates  []*domain.CleanupPolicy // records Update calls
}

func NewCleanupPolicyRepo(policies ...*domain.CleanupPolicy) *CleanupPolicyRepo {
	r := &CleanupPolicyRepo{policies: make(map[string]*domain.CleanupPolicy)}
	for _, p := range policies {
		r.policies[p.ID] = p
	}
	return r
}

func (r *CleanupPolicyRepo) List(_ context.Context) ([]domain.CleanupPolicy, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.CleanupPolicy, 0, len(r.policies))
	for _, v := range r.policies {
		out = append(out, *v)
	}
	return out, nil
}
func (r *CleanupPolicyRepo) Get(_ context.Context, id string) (*domain.CleanupPolicy, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.policies[id], nil
}
func (r *CleanupPolicyRepo) Create(_ context.Context, p *domain.CleanupPolicy) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	p.ID = fmt.Sprintf("policy-%d", r.nextID)
	r.policies[p.ID] = p
	return nil
}
func (r *CleanupPolicyRepo) Update(_ context.Context, p *domain.CleanupPolicy) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policies[p.ID] = p
	r.Updates = append(r.Updates, p)
	return nil
}
func (r *CleanupPolicyRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.policies, id)
	return nil
}

// ── BlobStore (storage) ───────────────────────────────────────

type BlobStore struct {
	mu      sync.Mutex
	blobs   map[string][]byte
	Deleted []string // records Delete calls
}

func NewBlobStore() *BlobStore {
	return &BlobStore{blobs: make(map[string][]byte)}
}

func (b *BlobStore) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.blobs[key] = data
	return nil
}
func (b *BlobStore) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.blobs[key]
	if !ok {
		return nil, 0, fmt.Errorf("blob not found: %s", key)
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}
func (b *BlobStore) Delete(_ context.Context, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.blobs, key)
	b.Deleted = append(b.Deleted, key)
	return nil
}
func (b *BlobStore) Exists(_ context.Context, key string) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.blobs[key]
	return ok, nil
}
func (b *BlobStore) Size(_ context.Context, key string) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.blobs[key]
	if !ok {
		return 0, fmt.Errorf("blob not found: %s", key)
	}
	return int64(len(data)), nil
}
func (b *BlobStore) UsedBytes(_ context.Context) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var total int64
	for _, data := range b.blobs {
		total += int64(len(data))
	}
	return total, nil
}
func (b *BlobStore) ListKeys(_ context.Context) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	keys := make([]string, 0, len(b.blobs))
	for k := range b.blobs {
		keys = append(keys, k)
	}
	return keys, nil
}
func (b *BlobStore) Has(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.blobs[key]
	return ok
}
func (b *BlobStore) Read(key string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.blobs[key]
	if !ok {
		return "", fmt.Errorf("not found")
	}
	return string(data), nil
}

// ── Helpers ───────────────────────────────────────────────────

// SimpleRepo returns a hosted repository ready for use in tests.
func SimpleRepo(name, format string) *domain.Repository {
	return &domain.Repository{
		ID:     "repo-" + name,
		Name:   name,
		Format: domain.RepoFormat(format),
		Type:   domain.TypeHosted,
		Online: true,
	}
}

// MakeReader returns an io.Reader wrapping the given string.
func MakeReader(s string) io.Reader { return strings.NewReader(s) }

// ── AuditRepo ─────────────────────────────────────────────────

type AuditRepo struct {
	mu     sync.Mutex
	Events []domain.AuditEvent
}

func NewAuditRepo() *AuditRepo { return &AuditRepo{} }

func (a *AuditRepo) Write(_ context.Context, e *domain.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Events = append(a.Events, *e)
	return nil
}

func (a *AuditRepo) match(e domain.AuditEvent, q repository.AuditQuery) bool {
	if q.Domain != "" && e.Domain != q.Domain {
		return false
	}
	if q.Action != "" && e.Action != q.Action {
		return false
	}
	if q.Username != "" && e.Username != q.Username {
		return false
	}
	if q.From != nil && e.EventTime.Before(*q.From) {
		return false
	}
	if q.To != nil && !e.EventTime.Before(*q.To) {
		return false
	}
	return true
}

func (a *AuditRepo) List(_ context.Context, q repository.AuditQuery) ([]domain.AuditEvent, int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var matched []domain.AuditEvent
	for _, e := range a.Events {
		if a.match(e, q) {
			matched = append(matched, e)
		}
	}
	total := len(matched)
	if q.Offset >= total {
		return nil, total, nil
	}
	matched = matched[q.Offset:]
	if q.Limit > 0 && len(matched) > q.Limit {
		matched = matched[:q.Limit]
	}
	return matched, total, nil
}

func (a *AuditRepo) Stream(_ context.Context, q repository.AuditQuery, fn func(domain.AuditEvent) error) error {
	a.mu.Lock()
	snapshot := append([]domain.AuditEvent(nil), a.Events...)
	a.mu.Unlock()
	for _, e := range snapshot {
		if !a.match(e, q) {
			continue
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

// ── UserRepo ──────────────────────────────────────────────────

type UserRepo struct {
	mu     sync.Mutex
	users  map[string]*domain.User // key: username
	byID   map[string]*domain.User
	nextID int
}

func NewUserRepo(users ...*domain.User) *UserRepo {
	r := &UserRepo{
		users: make(map[string]*domain.User),
		byID:  make(map[string]*domain.User),
	}
	for _, u := range users {
		r.users[u.Username] = u
		r.byID[u.ID] = u
	}
	return r
}

func (r *UserRepo) List(_ context.Context, _ string) ([]domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.User, 0, len(r.users))
	for _, u := range r.users {
		out = append(out, *u)
	}
	return out, nil
}
func (r *UserRepo) Get(_ context.Context, username string) (*domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.users[username], nil
}
func (r *UserRepo) GetByID(_ context.Context, id string) (*domain.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byID[id], nil
}
func (r *UserRepo) Create(_ context.Context, u *domain.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	if u.ID == "" {
		u.ID = fmt.Sprintf("user-%d", r.nextID)
	}
	r.users[u.Username] = u
	r.byID[u.ID] = u
	return nil
}
func (r *UserRepo) Update(_ context.Context, u *domain.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.users[u.Username] = u
	r.byID[u.ID] = u
	return nil
}
func (r *UserRepo) UpdatePassword(_ context.Context, username, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if u, ok := r.users[username]; ok {
		u.PasswordHash = hash
	}
	return nil
}
func (r *UserRepo) Delete(_ context.Context, username string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if u, ok := r.users[username]; ok {
		delete(r.byID, u.ID)
		delete(r.users, username)
	}
	return nil
}
func (r *UserRepo) UpdateLastLogin(_ context.Context, _ string) error { return nil }

// ── RoleRepo ──────────────────────────────────────────────────

type RoleRepo struct {
	mu         sync.Mutex
	roles      map[string]*domain.Role
	userRoles  map[string][]string // userID → roleIDs
	nextID     int
}

func NewRoleRepo(roles ...*domain.Role) *RoleRepo {
	r := &RoleRepo{
		roles:     make(map[string]*domain.Role),
		userRoles: make(map[string][]string),
	}
	for _, role := range roles {
		r.roles[role.ID] = role
	}
	return r
}

func (r *RoleRepo) List(_ context.Context) ([]domain.Role, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.Role, 0, len(r.roles))
	for _, v := range r.roles {
		out = append(out, *v)
	}
	return out, nil
}
func (r *RoleRepo) Get(_ context.Context, id string) (*domain.Role, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.roles[id], nil
}
func (r *RoleRepo) Create(_ context.Context, role *domain.Role) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	if role.ID == "" {
		role.ID = fmt.Sprintf("role-%d", r.nextID)
	}
	r.roles[role.ID] = role
	return nil
}
func (r *RoleRepo) Update(_ context.Context, role *domain.Role) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.roles[role.ID] = role
	return nil
}
func (r *RoleRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.roles, id)
	return nil
}
func (r *RoleRepo) GetUserRoles(_ context.Context, userID string) ([]domain.Role, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Role
	for _, rid := range r.userRoles[userID] {
		if role, ok := r.roles[rid]; ok {
			out = append(out, *role)
		}
	}
	return out, nil
}
func (r *RoleRepo) SetUserRoles(_ context.Context, userID string, roleIDs []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.userRoles[userID] = append([]string(nil), roleIDs...)
	return nil
}

func (r *RoleRepo) SetPrivileges(_ context.Context, roleID string, privilegeIDs []string) error {
	return nil
}

func (r *RoleRepo) ListPrivilegeIDsByRole(_ context.Context, roleID string) ([]string, error) {
	return []string{}, nil
}

// ── ContentSelectorRepo ───────────────────────────────────────

type ContentSelectorRepo struct {
	mu                sync.Mutex
	selectors         map[string]*domain.ContentSelector
	nextID            int
	PrivilegeSelector map[string]string   // privilegeName → selectorID
	UserSelectors     map[string][]string // userID → []selectorID
}

func NewContentSelectorRepo() *ContentSelectorRepo {
	return &ContentSelectorRepo{
		selectors:         make(map[string]*domain.ContentSelector),
		PrivilegeSelector: make(map[string]string),
		UserSelectors:     make(map[string][]string),
	}
}

func (r *ContentSelectorRepo) List(_ context.Context) ([]domain.ContentSelector, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.ContentSelector, 0, len(r.selectors))
	for _, v := range r.selectors {
		out = append(out, *v)
	}
	return out, nil
}
func (r *ContentSelectorRepo) Get(_ context.Context, id string) (*domain.ContentSelector, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.selectors[id], nil
}
func (r *ContentSelectorRepo) GetByName(_ context.Context, name string) (*domain.ContentSelector, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.selectors {
		if v.Name == name {
			cp := *v
			return &cp, nil
		}
	}
	return nil, nil
}
func (r *ContentSelectorRepo) Create(_ context.Context, s *domain.ContentSelector) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	s.ID = fmt.Sprintf("cs-%d", r.nextID)
	cp := *s
	r.selectors[s.ID] = &cp
	return nil
}
func (r *ContentSelectorRepo) Update(_ context.Context, s *domain.ContentSelector) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *s
	r.selectors[s.ID] = &cp
	return nil
}
func (r *ContentSelectorRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.selectors, id)
	return nil
}
func (r *ContentSelectorRepo) ListForUser(_ context.Context, userID string) ([]domain.ContentSelector, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.ContentSelector
	for _, id := range r.UserSelectors[userID] {
		if s, ok := r.selectors[id]; ok {
			out = append(out, *s)
		}
	}
	return out, nil
}
func (r *ContentSelectorRepo) AttachToPrivilege(_ context.Context, privilegeName, selectorID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.PrivilegeSelector[privilegeName] = selectorID
	return nil
}
func (r *ContentSelectorRepo) DetachFromPrivilege(_ context.Context, privilegeName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.PrivilegeSelector, privilegeName)
	return nil
}

// ── UserTokenRepo ─────────────────────────────────────────────

type UserTokenRepo struct {
	mu     sync.Mutex
	tokens map[string]*domain.UserToken // key: ID
	byHash map[string]*domain.UserToken // key: TokenHash
	nextID int
}

func NewUserTokenRepo() *UserTokenRepo {
	return &UserTokenRepo{
		tokens: make(map[string]*domain.UserToken),
		byHash: make(map[string]*domain.UserToken),
	}
}

func (r *UserTokenRepo) ListByUser(_ context.Context, userID string) ([]domain.UserToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.UserToken
	for _, t := range r.tokens {
		if t.UserID == userID {
			out = append(out, *t)
		}
	}
	return out, nil
}
func (r *UserTokenRepo) Get(_ context.Context, id string) (*domain.UserToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tokens[id], nil
}
func (r *UserTokenRepo) GetByHash(_ context.Context, hash string) (*domain.UserToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byHash[hash], nil
}
func (r *UserTokenRepo) Create(_ context.Context, t *domain.UserToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	if t.ID == "" {
		t.ID = fmt.Sprintf("tok-%d", r.nextID)
	}
	cp := *t
	r.tokens[t.ID] = &cp
	if t.TokenHash != "" {
		r.byHash[t.TokenHash] = &cp
	}
	return nil
}
func (r *UserTokenRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.tokens[id]; ok {
		delete(r.byHash, t.TokenHash)
		delete(r.tokens, id)
	}
	return nil
}
func (r *UserTokenRepo) TouchLastUsed(_ context.Context, _ string) error { return nil }

// ── WebhookRepo ───────────────────────────────────────────────

type WebhookRepo struct {
	mu       sync.Mutex
	webhooks map[string]*domain.Webhook
	nextID   int
}

func NewWebhookRepo() *WebhookRepo {
	return &WebhookRepo{webhooks: make(map[string]*domain.Webhook)}
}

func (r *WebhookRepo) List(_ context.Context) ([]domain.Webhook, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.Webhook, 0, len(r.webhooks))
	for _, v := range r.webhooks {
		out = append(out, *v)
	}
	return out, nil
}
func (r *WebhookRepo) Get(_ context.Context, id string) (*domain.Webhook, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.webhooks[id], nil
}
func (r *WebhookRepo) ListByEvent(_ context.Context, event domain.WebhookEvent) ([]domain.Webhook, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Webhook
	for _, v := range r.webhooks {
		for _, e := range v.Events {
			if e == event {
				out = append(out, *v)
				break
			}
		}
	}
	return out, nil
}
func (r *WebhookRepo) Create(_ context.Context, w *domain.Webhook) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	if w.ID == "" {
		w.ID = fmt.Sprintf("wh-%d", r.nextID)
	}
	cp := *w
	r.webhooks[w.ID] = &cp
	return nil
}
func (r *WebhookRepo) Update(_ context.Context, w *domain.Webhook) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *w
	r.webhooks[w.ID] = &cp
	return nil
}
func (r *WebhookRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.webhooks, id)
	return nil
}

// ── RoutingRuleRepo ───────────────────────────────────────────

type RoutingRuleRepo struct {
	mu     sync.Mutex
	rules  map[string]*domain.RoutingRule
	nextID int
}

func NewRoutingRuleRepo() *RoutingRuleRepo {
	return &RoutingRuleRepo{rules: make(map[string]*domain.RoutingRule)}
}

func (r *RoutingRuleRepo) List(_ context.Context) ([]domain.RoutingRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.RoutingRule, 0, len(r.rules))
	for _, v := range r.rules {
		out = append(out, *v)
	}
	return out, nil
}
func (r *RoutingRuleRepo) Get(_ context.Context, id string) (*domain.RoutingRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rules[id], nil
}
func (r *RoutingRuleRepo) GetByName(_ context.Context, name string) (*domain.RoutingRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.rules {
		if v.Name == name {
			cp := *v
			return &cp, nil
		}
	}
	return nil, nil
}
func (r *RoutingRuleRepo) Create(_ context.Context, rr *domain.RoutingRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	if rr.ID == "" {
		rr.ID = fmt.Sprintf("rr-%d", r.nextID)
	}
	cp := *rr
	r.rules[rr.ID] = &cp
	return nil
}
func (r *RoutingRuleRepo) Update(_ context.Context, rr *domain.RoutingRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *rr
	r.rules[rr.ID] = &cp
	return nil
}
func (r *RoutingRuleRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rules, id)
	return nil
}

// ── PrivilegeRepo ─────────────────────────────────────────────

type PrivilegeRepo struct {
	mu   sync.Mutex
	data map[string]*domain.Privilege
}

func NewPrivilegeRepo(items ...*domain.Privilege) *PrivilegeRepo {
	r := &PrivilegeRepo{data: make(map[string]*domain.Privilege)}
	for _, p := range items {
		r.data[p.ID] = p
	}
	return r
}

func (r *PrivilegeRepo) List(_ context.Context) ([]domain.Privilege, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.Privilege, 0, len(r.data))
	for _, p := range r.data {
		out = append(out, *p)
	}
	return out, nil
}

func (r *PrivilegeRepo) Get(_ context.Context, id string) (*domain.Privilege, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.data[id]
	if !ok {
		return nil, nil
	}
	cp := *p
	return &cp, nil
}

func (r *PrivilegeRepo) GetByName(_ context.Context, name string) (*domain.Privilege, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.data {
		if p.Name == name {
			cp := *p
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *PrivilegeRepo) Create(_ context.Context, p *domain.Privilege) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.ID == "" {
		p.ID = fmt.Sprintf("priv-%d", len(r.data)+1)
	}
	cp := *p
	r.data[p.ID] = &cp
	return nil
}

func (r *PrivilegeRepo) Update(_ context.Context, p *domain.Privilege) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[p.ID]; !ok {
		return fmt.Errorf("privilege not found: %s", p.ID)
	}
	cp := *p
	r.data[p.ID] = &cp
	return nil
}

func (r *PrivilegeRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	return nil
}

func (r *PrivilegeRepo) ListByRole(_ context.Context, _ string) ([]domain.Privilege, error) {
	return []domain.Privilege{}, nil
}

func (r *PrivilegeRepo) PrivilegeRoleMap(_ context.Context) (map[string][]string, error) {
	return map[string][]string{}, nil
}
