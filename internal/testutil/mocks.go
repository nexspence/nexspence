// Package testutil provides shared test helpers and in-memory mock implementations
// of repository and storage interfaces. It must only be imported from _test.go files.
package testutil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// ── compile-time interface assertions ────────────────────────
var (
	_ repository.RepositoryRepo    = (*RepoRepo)(nil)
	_ repository.BlobStoreRepo     = (*BlobStoreRepo)(nil)
	_ repository.ComponentRepo     = (*ComponentRepo)(nil)
	_ repository.AssetRepo         = (*AssetRepo)(nil)
	_ repository.CleanupPolicyRepo = (*CleanupPolicyRepo)(nil)
	_ storage.BlobStore            = (*BlobStore)(nil)
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

func (c *ComponentRepo) List(_ context.Context, _ string, _, _ int) (*domain.Page[domain.Component], error) {
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
func (c *ComponentRepo) Search(_ context.Context, _ domain.SearchParams) (*domain.Page[domain.Component], error) {
	return &domain.Page[domain.Component]{Items: []domain.Component{}}, nil
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
func (a *AssetRepo) ListStale(_ context.Context, _ string, _, _ int, _ int) ([]domain.Asset, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Stale, nil
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
