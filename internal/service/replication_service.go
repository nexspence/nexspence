package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// ReplicationService pushes artifacts from local repos to remote Nexspence instances.
type ReplicationService struct {
	repo       repository.ReplicationRepo
	assets     repository.AssetRepo
	blobStore  storage.BlobStore
	primaryKey []byte // seals all new ciphertexts
	legacyKey  []byte // sha256(jwt_secret) fallback; nil when no dedicated key is set
	log        logger.Logger

	mu            sync.Mutex
	cronScheduler *cron.Cron
	entryIDs      map[string]cron.EntryID
}

// NewReplicationService constructs a service that pushes artifacts to remote targets on a schedule.
func NewReplicationService(
	repo repository.ReplicationRepo,
	assets repository.AssetRepo,
	blobStore storage.BlobStore,
	jwtSecret string,
	encryptionKey []byte, // decoded auth.encryption_key; nil = legacy jwt-derived key
	log logger.Logger,
) *ReplicationService {
	s := &ReplicationService{
		repo:      repo,
		assets:    assets,
		blobStore: blobStore,
		log:       log,
		entryIDs:  make(map[string]cron.EntryID),
	}
	legacy := deriveKey(jwtSecret)
	if len(encryptionKey) == 32 {
		s.primaryKey = encryptionKey
		s.legacyKey = legacy
	} else {
		s.primaryKey = legacy
	}
	return s
}

// EncryptPassword encrypts plain with AES-256-GCM under the primary key.
// Returns base64url(nonce + ciphertext). Returns "" for empty plain.
func (s *ReplicationService) EncryptPassword(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	return sealWithKey(s.primaryKey, plain)
}

// DecryptPassword decrypts enc, falling back to the legacy jwt-derived key
// for rows sealed before auth.encryption_key was adopted.
func (s *ReplicationService) DecryptPassword(enc string) (string, error) {
	plain, _, err := s.decryptDetect(enc)
	return plain, err
}

// decryptDetect reports whether the legacy key was needed (true = the stored
// row should be re-encrypted under the primary key).
func (s *ReplicationService) decryptDetect(enc string) (string, bool, error) {
	if enc == "" {
		return "", false, nil
	}
	plain, err := openWithKey(s.primaryKey, enc)
	if err == nil {
		return plain, false, nil
	}
	if s.legacyKey != nil {
		if lp, lerr := openWithKey(s.legacyKey, enc); lerr == nil {
			return lp, true, nil
		}
	}
	return "", false, err
}

func sealWithKey(key []byte, plain string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.URLEncoding.EncodeToString(sealed), nil
}

func openWithKey(key []byte, enc string) (string, error) {
	data, err := base64.URLEncoding.DecodeString(enc)
	if err != nil {
		return "", fmt.Errorf("replication: base64 decode: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return "", fmt.Errorf("replication: ciphertext too short")
	}
	plain, err := gcm.Open(nil, data[:ns], data[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("replication: decrypt: %w", err)
	}
	return string(plain), nil
}

func deriveKey(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// ReEncryptCredentials migrates credentials sealed with the legacy jwt-derived
// key to the dedicated encryption key. Idempotent: rows already sealed with the
// primary key are skipped, so concurrent HA replicas and restarts are safe.
// Returns the number of migrated rules.
func (s *ReplicationService) ReEncryptCredentials(ctx context.Context) int {
	rules, err := s.repo.ListRules(ctx)
	if err != nil {
		s.log.Error("replication: re-encryption sweep: list rules", "err", err)
		return 0
	}
	migrated := 0
	for i := range rules {
		rule := rules[i]
		if rule.TargetPasswordEnc == "" {
			continue
		}
		plain, usedLegacy, err := s.decryptDetect(rule.TargetPasswordEnc)
		if err != nil {
			s.log.Warn("replication: credentials cannot be decrypted with any key — re-enter the password", "rule", rule.Name, "err", err)
			continue
		}
		if !usedLegacy {
			continue
		}
		enc, err := sealWithKey(s.primaryKey, plain)
		if err != nil {
			s.log.Error("replication: re-encrypt credentials", "rule", rule.Name, "err", err)
			continue
		}
		rule.TargetPasswordEnc = enc
		if err := s.repo.UpdateRule(ctx, &rule); err != nil {
			s.log.Error("replication: persist re-encrypted credentials", "rule", rule.Name, "err", err)
			continue
		}
		migrated++
	}
	if migrated > 0 {
		s.log.Info("replication: re-encrypted credentials under dedicated encryption key", "migrated", migrated, "rules", len(rules))
	}
	return migrated
}

// StartCronScheduler loads all enabled rules and registers cron jobs. Run as a goroutine.
func (s *ReplicationService) StartCronScheduler(ctx context.Context) {
	s.mu.Lock()
	s.cronScheduler = cron.New()
	s.mu.Unlock()

	rules, err := s.repo.ListRules(ctx)
	if err != nil {
		s.log.Error("replication: failed to load rules for scheduler", "err", err)
	} else {
		if s.legacyKey == nil && len(rules) > 0 {
			s.log.Warn("replication: stored credentials are encrypted with a key derived from auth.jwt_secret; set auth.encryption_key to decouple them (rotating jwt_secret would otherwise invalidate them)")
		}
		if s.legacyKey != nil {
			s.ReEncryptCredentials(ctx)
		}
		s.mu.Lock()
		for _, r := range rules {
			if r.Enabled {
				s.addEntryLocked(r)
			}
		}
		s.mu.Unlock()
	}

	s.cronScheduler.Start()
	<-ctx.Done()
	s.cronScheduler.Stop()
}

// ReloadRule updates the cron entry for a single rule (call after Create/Update/Delete).
func (s *ReplicationService) ReloadRule(ctx context.Context, ruleID string) {
	rule, _ := s.repo.GetRule(ctx, ruleID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cronScheduler == nil {
		return
	}
	if eid, ok := s.entryIDs[ruleID]; ok {
		s.cronScheduler.Remove(eid)
		delete(s.entryIDs, ruleID)
	}
	if rule == nil || !rule.Enabled {
		return
	}
	s.addEntryLocked(*rule)
}

func (s *ReplicationService) addEntryLocked(rule domain.ReplicationRule) {
	job := func() {
		if err := s.RunRule(context.Background(), rule.ID); err != nil {
			s.log.Error("replication cron error", "rule", rule.Name, "err", err)
		}
	}
	id, err := s.cronScheduler.AddFunc(rule.CronExpr, job)
	if err != nil {
		s.log.Warn("replication: invalid cron_expr, skipping rule", "rule", rule.Name, "expr", rule.CronExpr, "err", err)
		return
	}
	s.entryIDs[rule.ID] = id
}

// RunRule executes a single replication rule immediately (used by cron and manual trigger).
func (s *ReplicationService) RunRule(ctx context.Context, ruleID string) error {
	rule, err := s.repo.GetRule(ctx, ruleID)
	if err != nil {
		return err
	}
	if rule == nil {
		return fmt.Errorf("replication rule %q not found", ruleID)
	}

	_ = s.repo.UpdateRuleStatus(ctx, ruleID, "running", time.Now())

	hist := &domain.ReplicationHistory{
		RuleID:    ruleID,
		StartedAt: time.Now(),
	}

	runErr := s.runRule(ctx, rule, hist)

	now := time.Now()
	hist.FinishedAt = &now
	hist.DurationMs = now.Sub(hist.StartedAt).Milliseconds()

	status := "ok"
	if runErr != nil || hist.FailedCount > 0 {
		status = "error"
		if runErr != nil {
			hist.Error = runErr.Error()
		}
	}
	_ = s.repo.UpdateRuleStatus(ctx, ruleID, status, now)
	_ = s.repo.AddHistory(ctx, hist)

	return runErr
}

// runRule performs the actual diff + push for a rule.
func (s *ReplicationService) runRule(ctx context.Context, rule *domain.ReplicationRule, hist *domain.ReplicationHistory) error {
	password, err := s.DecryptPassword(rule.TargetPasswordEnc)
	if err != nil {
		return fmt.Errorf("decrypt credentials: %w", err)
	}

	targetPaths, err := s.listTargetPaths(ctx, rule, password)
	if err != nil {
		return fmt.Errorf("list target assets: %w", err)
	}

	localAssets, err := s.assets.ListByRepoAndPath(ctx, rule.SourceRepo, "")
	if err != nil {
		return fmt.Errorf("list local assets: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	for _, asset := range localAssets {
		if _, exists := targetPaths[asset.Path]; exists {
			hist.SkippedCount++
			continue
		}

		pushed, transferred, pushErr := s.pushAsset(ctx, client, rule, password, asset)
		if pushErr != nil {
			hist.FailedCount++
			if hist.Error == "" {
				hist.Error = pushErr.Error()
			}
			s.log.Warn("replication: push failed", "rule", rule.Name, "path", asset.Path, "err", pushErr)
			continue
		}
		if pushed {
			hist.PushedCount++
			hist.TransferredBytes += transferred
		}
	}
	return nil
}

// listTargetPaths queries the target instance for all asset paths in targetRepo.
func (s *ReplicationService) listTargetPaths(ctx context.Context, rule *domain.ReplicationRule, password string) (map[string]struct{}, error) {
	paths := make(map[string]struct{})
	client := &http.Client{Timeout: 30 * time.Second}
	token := ""

	for {
		url := strings.TrimRight(rule.TargetURL, "/") +
			"/service/rest/v1/assets?repository=" + rule.TargetRepo
		if token != "" {
			url += "&continuationToken=" + token
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		if rule.TargetUsername != "" {
			req.SetBasicAuth(rule.TargetUsername, password)
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("target returned %d: %s", resp.StatusCode, string(body))
		}

		var page struct {
			Items []struct {
				Path string `json:"path"`
			} `json:"items"`
			ContinuationToken *string `json:"continuationToken"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("parse target response: %w", err)
		}

		for _, item := range page.Items {
			paths[item.Path] = struct{}{}
		}

		if page.ContinuationToken == nil || *page.ContinuationToken == "" {
			break
		}
		token = *page.ContinuationToken
	}
	return paths, nil
}

// pushAsset streams one blob to the target. Returns (pushed, bytes, error).
func (s *ReplicationService) pushAsset(ctx context.Context, client *http.Client, rule *domain.ReplicationRule, password string, asset domain.Asset) (bool, int64, error) {
	rc, size, err := s.blobStore.Get(ctx, asset.BlobKey)
	if err != nil {
		return false, 0, fmt.Errorf("fetch blob %s: %w", asset.BlobKey, err)
	}
	defer func() { _ = rc.Close() }()

	targetPath := strings.TrimRight(rule.TargetURL, "/") +
		"/repository/" + rule.TargetRepo + "/" + strings.TrimPrefix(asset.Path, "/")

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, targetPath, rc)
	if err != nil {
		return false, 0, err
	}
	if size > 0 {
		req.ContentLength = size
	}
	if rule.TargetUsername != "" {
		req.SetBasicAuth(rule.TargetUsername, password)
	}
	if asset.ContentType != "" {
		req.Header.Set("Content-Type", asset.ContentType)
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, 0, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode >= 400 {
		return false, 0, fmt.Errorf("target PUT %s returned %d", asset.Path, resp.StatusCode)
	}
	return true, size, nil
}

// TestConnection verifies connectivity and credentials to a target rule.
func (s *ReplicationService) TestConnection(ctx context.Context, ruleID string) error {
	rule, err := s.repo.GetRule(ctx, ruleID)
	if err != nil {
		return err
	}
	if rule == nil {
		return fmt.Errorf("rule not found")
	}
	password, err := s.DecryptPassword(rule.TargetPasswordEnc)
	if err != nil {
		return err
	}

	url := strings.TrimRight(rule.TargetURL, "/") + "/service/rest/v1/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if rule.TargetUsername != "" {
		req.SetBasicAuth(rule.TargetUsername, password)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("target returned %d", resp.StatusCode)
	}
	return nil
}

// ListRules returns all replication rules (passwords masked).
func (s *ReplicationService) ListRules(ctx context.Context) ([]domain.ReplicationRule, error) {
	rules, err := s.repo.ListRules(ctx)
	if err != nil {
		return nil, err
	}
	for i := range rules {
		rules[i].TargetPasswordEnc = ""
	}
	return rules, nil
}

// GetRule returns a single rule (password masked).
func (s *ReplicationService) GetRule(ctx context.Context, id string) (*domain.ReplicationRule, error) {
	rule, err := s.repo.GetRule(ctx, id)
	if err != nil || rule == nil {
		return rule, err
	}
	rule.TargetPasswordEnc = ""
	return rule, nil
}

// CreateRule encrypts the password and persists the rule.
func (s *ReplicationService) CreateRule(ctx context.Context, rule *domain.ReplicationRule, plainPassword string) error {
	enc, err := s.EncryptPassword(plainPassword)
	if err != nil {
		return err
	}
	rule.TargetPasswordEnc = enc
	return s.repo.CreateRule(ctx, rule)
}

// UpdateRule encrypts the password if provided (non-empty), otherwise keeps existing.
func (s *ReplicationService) UpdateRule(ctx context.Context, rule *domain.ReplicationRule, plainPassword string) error {
	if plainPassword != "" {
		enc, err := s.EncryptPassword(plainPassword)
		if err != nil {
			return err
		}
		rule.TargetPasswordEnc = enc
	} else {
		existing, err := s.repo.GetRule(ctx, rule.ID)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return err
		}
		if existing != nil {
			rule.TargetPasswordEnc = existing.TargetPasswordEnc
		}
	}
	return s.repo.UpdateRule(ctx, rule)
}

// DeleteRule removes the rule and its cron entry.
func (s *ReplicationService) DeleteRule(ctx context.Context, id string) error {
	if err := s.repo.DeleteRule(ctx, id); err != nil {
		return err
	}
	s.ReloadRule(ctx, id)
	return nil
}

// ListHistory returns the last N history entries for a rule.
func (s *ReplicationService) ListHistory(ctx context.Context, ruleID string, limit int) ([]domain.ReplicationHistory, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.repo.ListHistory(ctx, ruleID, limit)
}
