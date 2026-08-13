package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3 implements AppendableBlobStore with a real multipart upload: each flush is
// an UploadPart, so a chunked push costs O(bytes pushed) instead of the
// O(N²) a read-modify-write over Put/Get costs (#214, #216).
//
// The awkward part is that S3 parts have a 5 MiB minimum (waived only for the
// last one) while a Docker client may PATCH chunks of any size. So AppendBlob
// keeps a sub-5 MiB tail in the session state and only uploads a part once that
// tail is big enough.
//
// Session state lives in a small JSON side-object next to the blob rather than
// in memory: the upload id, the completed parts, and the pending tail. That
// keeps the cross-instance/restart guarantee the local backend gives for free —
// any instance can continue a push another one started — and it stays
// proportional to the part count plus one sub-5 MiB buffer, never to the blob.
const (
	// s3MinPartSize is S3's minimum size for every part but the last.
	s3MinPartSize = 5 * 1024 * 1024
	// s3AppendReadChunk bounds how much of an incoming chunk is held at once, so
	// a client sending one enormous PATCH still costs ~5 MiB of memory, not its
	// whole chunk.
	s3AppendReadChunk = 256 * 1024
	// s3AppendMetaSuffix names the session side-object. Keys carrying it are
	// hidden from the listings, so GC never mistakes bookkeeping for a blob.
	s3AppendMetaSuffix = ".append-meta"
)

// s3AppendPart records one uploaded part; CompleteMultipartUpload needs the
// number and ETag of every one of them.
type s3AppendPart struct {
	Number int32  `json:"number"`
	ETag   string `json:"etag"`
	Size   int64  `json:"size"`
}

// s3AppendState is the durable state of one in-progress append.
type s3AppendState struct {
	UploadID string         `json:"upload_id"`
	Parts    []s3AppendPart `json:"parts"`
	Uploaded int64          `json:"uploaded"` // bytes in completed parts
	Pending  []byte         `json:"pending"`  // tail too small to be a part yet
}

// staged is how many bytes the caller has handed over so far, uploaded or not.
func (st *s3AppendState) staged() int64 { return st.Uploaded + int64(len(st.Pending)) }

func (s *S3BlobStore) appendMetaKey(key string) string {
	return s.objectKey(key) + s3AppendMetaSuffix
}

// AppendBlob appends r to the blob at key and returns the total staged so far.
// The bytes are not visible at key until FinalizeAppend runs — S3 shows nothing
// of a multipart upload until it is completed — which is why AppendedSize
// exists rather than callers reading Size.
func (s *S3BlobStore) AppendBlob(ctx context.Context, key string, r io.Reader) (int64, error) {
	st, err := s.loadAppendState(ctx, key)
	if errors.Is(err, errNoAppendSession) {
		st, err = s.startAppend(ctx, key)
	}
	if err != nil {
		return 0, err
	}

	buf := bytes.NewBuffer(st.Pending)
	window := make([]byte, s3AppendReadChunk)
	for {
		n, rerr := r.Read(window)
		if n > 0 {
			buf.Write(window[:n])
			// One read can add at most s3AppendReadChunk, so at most one part
			// becomes due per iteration and the buffer never exceeds
			// 5 MiB + one window.
			if buf.Len() >= s3MinPartSize {
				if err := s.flushPart(ctx, key, st, buf); err != nil {
					return 0, err
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return 0, fmt.Errorf("s3 append %s: %w", key, rerr)
		}
	}

	st.Pending = buf.Bytes()
	if err := s.saveAppendState(ctx, key, st); err != nil {
		return 0, err
	}
	return st.staged(), nil
}

// startAppend opens a multipart upload for key and seeds it with whatever the
// key already holds, so appending extends the blob exactly as it does on local
// disk. Without the seed, completing the upload would silently replace bytes a
// previous session had already published there.
func (s *S3BlobStore) startAppend(ctx context.Context, key string) (*s3AppendState, error) {
	out, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 create multipart upload %s: %w", key, err)
	}
	st := &s3AppendState{UploadID: aws.ToString(out.UploadId)}

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
	case size >= s3MinPartSize:
		// Big enough to be a part on its own, so it is copied server-side — the
		// bytes never travel through this process.
		if err := s.seedFromCopy(ctx, key, st, size); err != nil {
			return nil, err
		}
	default:
		// Too small to be a non-final part, so it becomes the pending tail.
		rc, _, err := s.Get(ctx, key)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()
		if st.Pending, err = io.ReadAll(rc); err != nil {
			return nil, fmt.Errorf("s3 append seed %s: %w", key, err)
		}
	}
	return st, nil
}

// seedFromCopy carries an existing object into the new upload as its first part
// via UploadPartCopy (server-side, no bytes through this process).
func (s *S3BlobStore) seedFromCopy(ctx context.Context, key string, st *s3AppendState, size int64) error {
	// Blob keys are machine-generated (hex digests, upload ids, the upload
	// prefix), so the copy source needs no percent-encoding.
	out, err := s.client.UploadPartCopy(ctx, &s3.UploadPartCopyInput{
		Bucket:     aws.String(s.bucket),
		Key:        aws.String(s.objectKey(key)),
		UploadId:   aws.String(st.UploadID),
		PartNumber: aws.Int32(1),
		CopySource: aws.String(s.bucket + "/" + s.objectKey(key)),
	})
	if err != nil {
		return fmt.Errorf("s3 append seed copy %s: %w", key, err)
	}
	etag := ""
	if out.CopyPartResult != nil {
		etag = aws.ToString(out.CopyPartResult.ETag)
	}
	st.Parts = append(st.Parts, s3AppendPart{Number: 1, ETag: etag, Size: size})
	st.Uploaded = size
	return nil
}

// flushPart uploads everything buffered as the next part and empties buf.
// Called with at least s3MinPartSize buffered, except from FinalizeAppend where
// the part is the last one and S3 waives the minimum.
func (s *S3BlobStore) flushPart(ctx context.Context, key string, st *s3AppendState, buf *bytes.Buffer) error {
	// buf.Reset keeps the backing array, which the request body would still be
	// aliasing, so the part gets its own copy.
	data := make([]byte, buf.Len())
	copy(data, buf.Bytes())
	buf.Reset()

	number := int32(len(st.Parts)) + 1 //nolint:gosec // part count is bounded by S3's 10k limit
	out, err := s.client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(s.objectKey(key)),
		UploadId:      aws.String(st.UploadID),
		PartNumber:    aws.Int32(number),
		Body:          bytes.NewReader(data),
		ContentLength: aws.Int64(int64(len(data))),
	})
	if err != nil {
		return fmt.Errorf("s3 upload part %d of %s: %w", number, key, err)
	}
	st.Parts = append(st.Parts, s3AppendPart{
		Number: number, ETag: aws.ToString(out.ETag), Size: int64(len(data)),
	})
	st.Uploaded += int64(len(data))
	return nil
}

// TruncateBlob discards staged bytes past size.
//
// Known limitation: only the pending tail can be discarded. Bytes already
// uploaded as a part are immutable, and rolling back past one would mean
// aborting the whole multipart upload — throwing away every part, not just the
// last. That is only reachable when the byte crossing a caller's size cap also
// crosses a 5 MiB part boundary in the same append, so this fails loudly rather
// than quietly dropping data the caller expected to keep.
func (s *S3BlobStore) TruncateBlob(ctx context.Context, key string, size int64) error {
	st, err := s.loadAppendState(ctx, key)
	if errors.Is(err, errNoAppendSession) {
		return fmt.Errorf("s3 truncate %s: %w", key, err)
	}
	if err != nil {
		return err
	}
	if size > st.staged() {
		return fmt.Errorf("s3 truncate %s: %d is past the %d bytes staged", key, size, st.staged())
	}
	if size < st.Uploaded {
		return fmt.Errorf("s3 truncate %s: %d bytes are already committed as multipart parts "+
			"and cannot be rolled back without discarding the whole upload", key, st.Uploaded)
	}
	st.Pending = st.Pending[:size-st.Uploaded]
	return s.saveAppendState(ctx, key, st)
}

// AppendedSize reports the bytes staged for key. An in-progress multipart
// upload is invisible at the key itself — HeadObject would keep answering with
// the empty object the session started from — so the session state answers
// whenever there is one.
func (s *S3BlobStore) AppendedSize(ctx context.Context, key string) (int64, bool, error) {
	st, err := s.loadAppendState(ctx, key)
	if err == nil {
		return st.staged(), true, nil
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

// FinalizeAppend publishes the appended bytes at key. The pending tail goes up
// as the final part, where S3 waives the 5 MiB minimum.
func (s *S3BlobStore) FinalizeAppend(ctx context.Context, key string) error {
	st, err := s.loadAppendState(ctx, key)
	if errors.Is(err, errNoAppendSession) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(st.Pending) > 0 {
		buf := bytes.NewBuffer(st.Pending)
		if err := s.flushPart(ctx, key, st, buf); err != nil {
			return err
		}
		st.Pending = nil
	}
	if len(st.Parts) == 0 {
		// Nothing was ever appended. CompleteMultipartUpload rejects an empty
		// part list, so the session is dropped and the empty object written
		// directly — the result a zero-byte blob must have either way.
		if err := s.abortMultipart(ctx, key, st.UploadID); err != nil {
			return err
		}
		if err := s.Put(ctx, key, bytes.NewReader(nil), 0); err != nil {
			return err
		}
		return s.deleteAppendState(ctx, key)
	}

	parts := make([]types.CompletedPart, 0, len(st.Parts))
	for _, p := range st.Parts {
		parts = append(parts, types.CompletedPart{
			PartNumber: aws.Int32(p.Number),
			ETag:       aws.String(p.ETag),
		})
	}
	if _, err := s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(s.bucket),
		Key:             aws.String(s.objectKey(key)),
		UploadId:        aws.String(st.UploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
	}); err != nil {
		return fmt.Errorf("s3 complete multipart upload %s: %w", key, err)
	}
	return s.deleteAppendState(ctx, key)
}

// AbortAppend drops an unfinished append. This is not merely tidiness: parts of
// an abandoned multipart upload stay billable in the bucket indefinitely, and
// nothing else reclaims them — they are invisible to ListObjects, so the blob
// GC sweep cannot see them either.
func (s *S3BlobStore) AbortAppend(ctx context.Context, key string) error {
	st, err := s.loadAppendState(ctx, key)
	if errors.Is(err, errNoAppendSession) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.abortMultipart(ctx, key, st.UploadID); err != nil {
		return err
	}
	return s.deleteAppendState(ctx, key)
}

func (s *S3BlobStore) abortMultipart(ctx context.Context, key, uploadID string) error {
	_, err := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(s.bucket),
		Key:      aws.String(s.objectKey(key)),
		UploadId: aws.String(uploadID),
	})
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("s3 abort multipart upload %s: %w", key, err)
	}
	return nil
}

// errNoAppendSession reports that a key has no append in progress. Every entry
// point treats that as a normal state — an idle key, a finished session, a
// second finalize — not a failure.
var errNoAppendSession = errors.New("no append session")

// loadAppendState returns the session for key, or errNoAppendSession.
func (s *S3BlobStore) loadAppendState(ctx context.Context, key string) (*s3AppendState, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.appendMetaKey(key)),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, errNoAppendSession
		}
		return nil, fmt.Errorf("s3 read append state %s: %w", key, err)
	}
	defer func() { _ = out.Body.Close() }()
	var st s3AppendState
	if err := json.NewDecoder(out.Body).Decode(&st); err != nil {
		return nil, fmt.Errorf("s3 decode append state %s: %w", key, err)
	}
	return &st, nil
}

func (s *S3BlobStore) saveAppendState(ctx context.Context, key string, st *s3AppendState) error {
	body, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("s3 encode append state %s: %w", key, err)
	}
	if _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(s.appendMetaKey(key)),
		Body:          bytes.NewReader(body),
		ContentLength: aws.Int64(int64(len(body))),
	}); err != nil {
		return fmt.Errorf("s3 write append state %s: %w", key, err)
	}
	return nil
}

func (s *S3BlobStore) deleteAppendState(ctx context.Context, key string) error {
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.appendMetaKey(key)),
	}); err != nil && !isNotFound(err) {
		return fmt.Errorf("s3 delete append state %s: %w", key, err)
	}
	return nil
}

// isAppendMetaObject reports whether an object key is append bookkeeping rather
// than a blob. Listings hide those: counted as blobs they would inflate usage
// and, worse, offer GC an "orphan" whose deletion would strand the multipart
// parts it is the only record of.
func isAppendMetaObject(objectKey string) bool {
	return strings.HasSuffix(objectKey, s3AppendMetaSuffix)
}
