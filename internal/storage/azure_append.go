package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
)

// Azure implements AppendableBlobStore with staged blocks of a block blob:
// each flush is a PutBlock, so a chunked push costs O(bytes pushed) instead
// of the O(N²) a read-modify-write over Put/Get costs — the same design the
// S3 backend uses with multipart parts (#214, #216).
//
// Azure's native Append Blob is deliberately NOT used: it caps at 4,000
// blocks (50 MiB at the 4 MiB staging floor, ~780 MiB at the 190 MiB
// practical chunk), far below the OCI layers this path exists for. Staged
// blocks have no such practical ceiling (50,000 × block size).
//
// Session state lives in a small JSON side-blob next to the blob — the same
// <objkey>.append-meta object the S3 backend writes — holding the staged
// block IDs and the pending sub-block tail. No in-process state, so any
// replica can continue a push another one started.
const (
	// azureMinBlockSize is the staging floor. Azure has no minimum block
	// size on paper, but blocking at 4 MiB keeps block counts low (the
	// 50,000-block cap bounds a blob at ~190 GiB here) and keeps each
	// PUT cheap; sub-4 MiB PATCHes accumulate in the pending tail first.
	azureMinBlockSize = 4 * 1024 * 1024
	// azureAppendReadChunk bounds how much of an incoming chunk is held at
	// once, mirroring s3AppendReadChunk.
	azureAppendReadChunk = 256 * 1024
)

// azureAppendState is the durable state of one in-progress append.
type azureAppendState struct {
	Session  string   `json:"session"`   // random id prefixing this session's block IDs
	BlockIDs []string `json:"block_ids"` // base64 block IDs, staged and uncommitted
	Staged   int64    `json:"staged"`    // bytes inside those blocks, excluding the seed
	Pending  []byte   `json:"pending"`   // tail too small to stage yet
	// SeedSize is only decoded, never written: sessions from before the
	// seed was folded into Staged kept it separate, and loadAppendState
	// migrates those in place. Keeping the tag lets such a session finish
	// across an upgrade instead of losing its offset mid-push.
	SeedSize int64 `json:"seed_size,omitempty"`
}

// stagedTotal is how many bytes the blob will hold once this session commits:
// everything inside its blocks — the seed's included, exactly as the S3 path
// counts a seeded part in Uploaded — plus the tail not staged yet. Callers use
// it as the session's offset, so leaving the seed out would under-report the
// resume point of a push onto an existing blob.
func (st *azureAppendState) stagedTotal() int64 { return st.Staged + int64(len(st.Pending)) }

func (s *AzureBlobStore) appendMetaKey(key string) string {
	return s.objectKey(key) + appendMetaSuffix
}

// AppendBlob appends r to the blob at key and returns the total staged so
// far. Nothing is visible at key until FinalizeAppend commits the block
// list — which is why AppendedSize exists rather than callers reading Size.
func (s *AzureBlobStore) AppendBlob(ctx context.Context, key string, r io.Reader) (int64, error) {
	st, err := s.loadAppendState(ctx, key)
	if errors.Is(err, errNoAppendSession) {
		st, err = s.startAppend(ctx, key)
	}
	if err != nil {
		return 0, err
	}

	bc := s.container.NewBlockBlobClient(s.objectKey(key))
	buf := bytes.NewBuffer(st.Pending)
	window := make([]byte, azureAppendReadChunk)
	for {
		n, rerr := r.Read(window)
		if n > 0 {
			buf.Write(window[:n])
			// One read adds at most one window, so at most one block
			// becomes due per iteration and the buffer never exceeds
			// 4 MiB + one window.
			if buf.Len() >= azureMinBlockSize {
				if err := s.stageBlock(ctx, bc, key, st, buf); err != nil {
					return 0, err
				}
				// A staged block is real, billable Azure storage the
				// moment PutBlock returns — invisible to listings, so
				// nothing but this side-blob can ever reclaim it. Record
				// it before anything later in this call can fail (a read
				// error on the request body, a later block's own upload
				// failure), exactly as #370 taught the S3 path.
				st.Pending = buf.Bytes()
				if err := s.saveAppendState(ctx, key, st); err != nil {
					return 0, err
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return 0, fmt.Errorf("azure append %s: %w", key, rerr)
		}
	}

	st.Pending = buf.Bytes()
	if err := s.saveAppendState(ctx, key, st); err != nil {
		return 0, err
	}
	return st.stagedTotal(), nil
}

// startAppend opens a session for key and seeds it with whatever the key
// already holds, so appending extends the blob exactly as it does on local
// disk. Existing committed blocks are carried into the eventual commit
// list; a single-PUT blob below the staging floor becomes the pending tail,
// and one at or above it is re-staged (seedFromStream) because Azure gives no
// way to inherit blocks a Put Blob never created.
func (s *AzureBlobStore) startAppend(ctx context.Context, key string) (*azureAppendState, error) {
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return nil, fmt.Errorf("azure append %s: session id: %w", key, err)
	}
	st := &azureAppendState{Session: base64.RawURLEncoding.EncodeToString(id)}

	bc := s.container.NewBlockBlobClient(s.objectKey(key))
	exists, err := s.Exists(ctx, key)
	if err != nil {
		return nil, err
	}
	if !exists {
		return st, nil
	}
	size, err := s.Size(ctx, key)
	if err != nil {
		return nil, err
	}
	switch {
	case size == 0:
		// The empty object an upload session starts from: nothing to carry over.
	case size >= azureMinBlockSize:
		// A block-blob upload: its committed blocks simply join the
		// commit list later, so its bytes never travel through this
		// process. Only a blob without a block list has to be re-staged.
		committed, err := bc.GetBlockList(ctx, blockblob.BlockListTypeCommitted, nil)
		if err != nil {
			return nil, fmt.Errorf("azure append seed %s: block list: %w", key, redactAzureError(err))
		}
		if committed.CommittedBlocks != nil {
			for _, b := range committed.CommittedBlocks {
				if b == nil || b.Name == nil {
					continue
				}
				st.BlockIDs = append(st.BlockIDs, *b.Name)
				if b.Size != nil {
					st.Staged += *b.Size
				}
			}
			// Staged now holds the seed's bytes, which is what the
			// commit will publish — the same meaning the S3 path gives
			// Uploaded after a seed part copy.
		} else {
			// No block list at this key: it was written in one Put Blob
			// (a presigned upload, a server-side copy, another tool), so
			// there are no blocks to inherit. Re-stage its bytes instead —
			// never io.ReadAll them, the blob can be MaxUploadBytes large.
			if err := s.seedFromStream(ctx, key, st, bc); err != nil {
				return nil, err
			}
		}
	default:
		// A blob below the staging floor was written in one PUT — it is
		// the pending tail.
		if err := s.seedFromRead(ctx, key, st); err != nil {
			return nil, err
		}
	}
	return st, nil
}

// seedFromRead downloads the existing blob into the session's pending tail.
// Only ever called below the staging floor, so the read is bounded by
// azureMinBlockSize — a blob at or above it goes through seedFromStream.
func (s *AzureBlobStore) seedFromRead(ctx context.Context, key string, st *azureAppendState) error {
	rc, _, err := s.Get(ctx, key)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	if st.Pending, err = io.ReadAll(rc); err != nil {
		return fmt.Errorf("azure append seed %s: %w", key, err)
	}
	// The seed now lives inside Pending, where stagedTotal already counts
	// it — recording it anywhere else would count it twice.
	return nil
}

// seedFromStream re-stages a blob that has no committed block list into this
// session's own blocks, keeping the pending tail as the remainder. Costs one
// extra download plus upload of the existing bytes, which is the price of a
// blob nothing staged in blocks — but memory stays at one block, where
// io.ReadAll would hold a whole OCI layer (up to MaxUploadBytes, 10 GiB by
// default) in RAM, the trap #385 already sprang once on the OCI finalize path.
func (s *AzureBlobStore) seedFromStream(ctx context.Context, key string, st *azureAppendState, bc *blockblob.Client) error {
	rc, _, err := s.Get(ctx, key)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()

	buf := bytes.NewBuffer(make([]byte, 0, azureMinBlockSize))
	window := make([]byte, azureAppendReadChunk)
	for {
		n, rerr := rc.Read(window)
		if n > 0 {
			buf.Write(window[:n])
			if buf.Len() >= azureMinBlockSize {
				if serr := s.stageBlock(ctx, bc, key, st, buf); serr != nil {
					return serr
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("azure append seed %s: %w", key, rerr)
		}
	}
	// stageBlock already counted the re-staged bytes in Staged; the
	// sub-block remainder stays the pending tail.
	st.Pending = buf.Bytes()
	return nil
}

// azureBlockIDWidth is the raw byte width new block IDs are padded to. Azure
// requires every block ID of one blob to decode to the SAME number of bytes
// and answers 400 InvalidBlockList otherwise, so a session that inherits
// blocks staged by the SDK (blockblob.UploadStream uses a 64-byte id) has to
// speak that width too. 64 is also the maximum Azure allows. Azurite accepts
// mixed widths, so no emulator test can catch a regression here — the unit
// test on blockIDWidth is the guard.
const azureBlockIDWidth = 64

// blockIDWidth is the raw ID width this session must keep using: whatever the
// IDs already in the commit list decode to, else the 64-byte default. Seeded
// SDK blocks decode to 64; a session written by an older Nexspence decodes to
// 18, and staying at 18 keeps that in-flight upload committable across the
// upgrade.
func (st *azureAppendState) blockIDWidth() int {
	if len(st.BlockIDs) == 0 {
		return azureBlockIDWidth
	}
	raw, err := base64.StdEncoding.DecodeString(st.BlockIDs[0])
	if err != nil || len(raw) == 0 || len(raw) > azureBlockIDWidth {
		return azureBlockIDWidth
	}
	return len(raw)
}

// nextBlockID builds a deterministic per-session block ID: session id plus a
// sequence number, zero-padded to the session's ID width and base64-encoded
// as Azure requires. Replaying the same state re-derives the same IDs; a
// stray stage from a crashed attempt is simply invisible until (and unless) a
// commit names it.
func (st *azureAppendState) nextBlockID() (string, error) {
	id := fmt.Sprintf("%s-%06d", st.Session, len(st.BlockIDs))
	width := st.blockIDWidth()
	if len(id) > width {
		return "", fmt.Errorf("azure block id %q exceeds the %d-byte width already used by this blob", id, width)
	}
	padded := make([]byte, width)
	copy(padded, id)
	return base64.StdEncoding.EncodeToString(padded), nil
}

// stageBlock stages everything buffered as the next block and empties buf.
func (s *AzureBlobStore) stageBlock(ctx context.Context, bc *blockblob.Client, key string, st *azureAppendState, buf *bytes.Buffer) error {
	// buf.Reset keeps the backing array the request body would still
	// alias, so the block gets its own copy.
	data := make([]byte, buf.Len())
	copy(data, buf.Bytes())
	buf.Reset()

	blockID, err := st.nextBlockID()
	if err != nil {
		return fmt.Errorf("azure stage block of %s: %w", key, err)
	}
	if _, err := bc.StageBlock(ctx, blockID, nopSeekCloser{bytes.NewReader(data)}, nil); err != nil {
		return fmt.Errorf("azure stage block of %s: %w", key, redactAzureError(err))
	}
	st.BlockIDs = append(st.BlockIDs, blockID)
	st.Staged += int64(len(data))
	return nil
}

// nopSeekCloser adapts a *bytes.Reader to the io.ReadSeekCloser StageBlock
// asks for; the buffer is owned, so Close is a no-op.
type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }

// TruncateBlob discards staged bytes past size.
//
// Known limitation (identical to the S3 path): only the pending tail can be
// discarded. Staged blocks are immutable once PUT; rolling back past one
// would mean abandoning the whole session — throwing away every block, not
// just the last. Only reachable when crossing a caller's size cap also
// crosses a 4 MiB block boundary in the same append, so this fails loudly
// rather than quietly dropping data the caller expected to keep.
func (s *AzureBlobStore) TruncateBlob(ctx context.Context, key string, size int64) error {
	st, err := s.loadAppendState(ctx, key)
	if errors.Is(err, errNoAppendSession) {
		return fmt.Errorf("azure truncate %s: %w", key, err)
	}
	if err != nil {
		return err
	}
	if size > st.stagedTotal() {
		return fmt.Errorf("azure truncate %s: %d is past the %d bytes staged", key, size, st.stagedTotal())
	}
	if size < st.Staged {
		return fmt.Errorf("azure truncate %s: %d bytes are already staged as blocks "+
			"and cannot be rolled back without discarding the whole upload", key, st.Staged)
	}
	st.Pending = st.Pending[:size-st.Staged]
	return s.saveAppendState(ctx, key, st)
}

// AppendedSize reports the bytes staged for key. An open append session's
// blocks are invisible at the blob itself — GetProperties would keep
// answering with the pre-session state — so the session state answers
// whenever there is one.
func (s *AzureBlobStore) AppendedSize(ctx context.Context, key string) (int64, bool, error) {
	st, err := s.loadAppendState(ctx, key)
	if err == nil {
		return st.stagedTotal(), true, nil
	}
	if !errors.Is(err, errNoAppendSession) {
		return 0, false, err
	}
	exists, err := s.Exists(ctx, key)
	if err != nil || !exists {
		return 0, false, err
	}
	size, err := s.Size(ctx, key)
	if err != nil {
		return 0, false, err
	}
	return size, true, nil
}

// FinalizeAppend publishes the appended bytes at key. The pending tail goes
// up as one last block (Azure waives nothing here — it has no minimum — so
// even a small tail stages cleanly), then the commit list becomes the
// seed's committed blocks followed by this session's.
func (s *AzureBlobStore) FinalizeAppend(ctx context.Context, key string) error {
	st, err := s.loadAppendState(ctx, key)
	if errors.Is(err, errNoAppendSession) {
		return nil
	}
	if err != nil {
		return err
	}
	bc := s.container.NewBlockBlobClient(s.objectKey(key))

	if len(st.Pending) > 0 {
		buf := bytes.NewBuffer(st.Pending)
		if err := s.stageBlock(ctx, bc, key, st, buf); err != nil {
			return err
		}
		st.Pending = nil
	}
	if len(st.BlockIDs) == 0 {
		// Nothing staged and no pending tail: the session carried zero
		// bytes (a truncate back to 0, or a session opened on an empty
		// blob and never fed). Publish the empty result the caller
		// expects, then drop the bookkeeping.
		if err := s.Put(ctx, key, bytes.NewReader(nil), 0); err != nil {
			return err
		}
		return s.deleteAppendState(ctx, key)
	}

	if _, err := bc.CommitBlockList(ctx, st.BlockIDs, nil); err != nil {
		return fmt.Errorf("azure commit block list %s: %w", key, redactAzureError(err))
	}
	return s.deleteAppendState(ctx, key)
}

// AbortAppend drops an unfinished append's bookkeeping.
//
// Documented deviation from S3: AbortMultipartUpload frees parts
// immediately; Azure staged blocks that are never committed are invisible
// to every listing and are garbage-collected by the service after ~7 days.
// They are billable until then and cannot be deleted individually. The
// side-blob is the only handle we have, so removing it is all AbortAppend
// can do — hence Delete's best-effort AbortAppend first, and GC treating
// abandoned sessions as reclaimable immediately.
func (s *AzureBlobStore) AbortAppend(ctx context.Context, key string) error {
	err := s.deleteAppendState(ctx, key)
	if errors.Is(err, errNoAppendSession) {
		return nil
	}
	return err
}

func (s *AzureBlobStore) loadAppendState(ctx context.Context, key string) (*azureAppendState, error) {
	res, err := s.container.NewBlobClient(s.appendMetaKey(key)).DownloadStream(ctx, nil)
	if err != nil {
		if isAzureNotFound(err) {
			return nil, errNoAppendSession
		}
		return nil, fmt.Errorf("azure read append state %s: %w", key, redactAzureError(err))
	}
	defer func() { _ = res.Body.Close() }()
	var st azureAppendState
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		return nil, fmt.Errorf("azure decode append state %s: %w", key, err)
	}
	if st.SeedSize > 0 {
		// An in-flight session written before the seed was folded into
		// Staged. Migrate it here so every size computation downstream has
		// one meaning, and so the push can finish across the upgrade.
		st.Staged += st.SeedSize
		st.SeedSize = 0
	}
	return &st, nil
}

func (s *AzureBlobStore) saveAppendState(ctx context.Context, key string, st *azureAppendState) error {
	body, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("azure encode append state %s: %w", key, err)
	}
	_, err = s.container.NewBlockBlobClient(s.appendMetaKey(key)).UploadBuffer(ctx, body, nil)
	if err != nil {
		return fmt.Errorf("azure write append state %s: %w", key, redactAzureError(err))
	}
	return nil
}

func (s *AzureBlobStore) deleteAppendState(ctx context.Context, key string) error {
	_, err := s.container.NewBlobClient(s.appendMetaKey(key)).Delete(ctx, nil)
	if err != nil && !isAzureNotFound(err) {
		return fmt.Errorf("azure delete append state %s: %w", key, redactAzureError(err))
	}
	return nil
}
