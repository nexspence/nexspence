//go:build ignore

// migrate_blob_keys.go re-keys blobs uploaded before Phase 18 from path-addressed
// keys (SHA256("repoName:filePath")) to content-addressed keys (SHA256(file content)).
//
// Run once after deploying Phase 18 on an existing installation:
//
//	go run scripts/migrate_blob_keys.go \
//	    --dsn "postgres://user:pass@localhost/nexspence" \
//	    --blobs /var/lib/nexspence/blobs
//
// The script:
//  1. Reads every row from assets where blob_key length == 64 and the key
//     is NOT already in global_blobs (i.e., it's an old path-derived key).
//  2. Opens the physical blob file at the old key path.
//  3. Computes the SHA-256 of the content.
//  4. If the new key already exists on disk: deletes the old file.
//     If not: renames the old file to the new key path.
//  5. Updates assets.blob_key = new_key for the asset row.
//  6. Upserts global_blobs(blob_key, size_bytes, ref_count).
//
// Safe to re-run: already-migrated assets have a global_blobs row and are skipped.
// Dry-run mode (--dry-run) prints actions without modifying anything.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dsn    := flag.String("dsn", "", "PostgreSQL DSN (required)")
	blobs  := flag.String("blobs", "./blobs", "Blob store base directory")
	dryRun := flag.Bool("dry-run", false, "Print actions without modifying")
	flag.Parse()

	if *dsn == "" {
		log.Fatal("--dsn is required")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// Fetch assets whose blob_key is not yet in global_blobs.
	rows, err := pool.Query(ctx, `
		SELECT a.id, a.blob_key, a.size_bytes
		FROM assets a
		LEFT JOIN global_blobs g ON g.blob_key = a.blob_key
		WHERE g.blob_key IS NULL
		  AND LENGTH(a.blob_key) = 64
		  AND a.blob_key ~ '^[0-9a-f]{64}$'
	`)
	if err != nil {
		log.Fatalf("query assets: %v", err)
	}
	defer rows.Close()

	type row struct {
		id      string
		blobKey string
		size    int64
	}
	var assets []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.blobKey, &r.size); err != nil {
			log.Printf("scan row: %v", err)
			continue
		}
		assets = append(assets, r)
	}
	rows.Close()

	log.Printf("Found %d asset(s) to migrate", len(assets))

	ok, skipped, failed := 0, 0, 0
	for _, a := range assets {
		newKey, err := rekey(*blobs, a.blobKey, *dryRun)
		if err != nil {
			log.Printf("SKIP %s (%s): %v", a.id, a.blobKey, err)
			failed++
			continue
		}
		if newKey == a.blobKey {
			skipped++ // already content-addressed or file missing
			continue
		}
		if *dryRun {
			log.Printf("DRY-RUN: asset %s %s → %s", a.id, a.blobKey, newKey)
			ok++
			continue
		}
		// Update asset record.
		if _, err := pool.Exec(ctx, `UPDATE assets SET blob_key=$1 WHERE id=$2`, newKey, a.id); err != nil {
			log.Printf("FAIL update asset %s: %v", a.id, err)
			failed++
			continue
		}
		// Upsert global_blobs.
		if _, err := pool.Exec(ctx, `
			INSERT INTO global_blobs (blob_key, size_bytes, ref_count)
			VALUES ($1, $2, 1)
			ON CONFLICT (blob_key) DO UPDATE SET ref_count = global_blobs.ref_count + 1
		`, newKey, a.size); err != nil {
			log.Printf("FAIL upsert global_blobs %s: %v", newKey, err)
		}
		log.Printf("OK %s %s → %s", a.id, a.blobKey, newKey)
		ok++
	}
	log.Printf("Done. migrated=%d skipped=%d failed=%d", ok, skipped, failed)
}

// rekey reads the blob at oldKey, computes content SHA-256, moves the file
// to the new key path and returns the new key.
func rekey(basePath, oldKey string, dryRun bool) (string, error) {
	oldPath := keyPath(basePath, oldKey)
	f, err := os.Open(oldPath)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", oldPath, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash: %w", err)
	}
	newKey := hex.EncodeToString(h.Sum(nil))
	if newKey == oldKey {
		return oldKey, nil // already content-addressed
	}

	newPath := keyPath(basePath, newKey)
	if dryRun {
		return newKey, nil
	}
	if _, err := os.Stat(newPath); err == nil {
		// New key already exists — just remove the old file.
		return newKey, os.Remove(oldPath)
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return "", err
	}
	return newKey, os.Rename(oldPath, newPath)
}

func keyPath(base, key string) string {
	if len(key) >= 4 {
		return filepath.Join(base, key[:2], key[2:4], key)
	}
	return filepath.Join(base, key)
}
