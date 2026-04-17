package storage

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3BlobStore stores blobs as S3 objects.
// Keys are stored as object keys with a two-level prefix shard: ab/cd/<key>.
// Compatible with AWS S3, MinIO, Ceph S3, and any S3-compatible API.
type S3BlobStore struct {
	client         *s3.Client
	bucket         string
	forcePathStyle bool
}

// S3Options configures the S3 blob store.
type S3Options struct {
	Bucket          string
	Region          string
	Endpoint        string // custom endpoint for MinIO / Ceph
	AccessKeyID     string
	SecretAccessKey string
	ForcePathStyle  bool
}

// NewS3BlobStore creates an S3BlobStore and validates connectivity by checking the bucket.
func NewS3BlobStore(ctx context.Context, opts S3Options) (*S3BlobStore, error) {
	if opts.Bucket == "" {
		return nil, fmt.Errorf("s3: bucket name is required")
	}

	cfgOpts := []func(*config.LoadOptions) error{
		config.WithRegion(opts.Region),
	}

	if opts.AccessKeyID != "" && opts.SecretAccessKey != "" {
		cfgOpts = append(cfgOpts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(opts.AccessKeyID, opts.SecretAccessKey, ""),
		))
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, cfgOpts...)
	if err != nil {
		return nil, fmt.Errorf("s3: load config: %w", err)
	}

	s3Opts := []func(*s3.Options){}
	if opts.Endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(opts.Endpoint)
		})
	}
	if opts.ForcePathStyle {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.UsePathStyle = true
		})
	}

	client := s3.NewFromConfig(awsCfg, s3Opts...)

	return &S3BlobStore{
		client:         client,
		bucket:         opts.Bucket,
		forcePathStyle: opts.ForcePathStyle,
	}, nil
}

// objectKey shards the blob key into a two-level prefix to avoid hot-spotting.
func (s *S3BlobStore) objectKey(key string) string {
	if len(key) >= 4 {
		return key[:2] + "/" + key[2:4] + "/" + key
	}
	return key
}

func (s *S3BlobStore) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
		Body:   r,
	}
	if size > 0 {
		input.ContentLength = aws.Int64(size)
	}
	_, err := s.client.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	return nil
}

func (s *S3BlobStore) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, 0, fmt.Errorf("blob not found: %s", key)
		}
		return nil, 0, fmt.Errorf("s3 get %s: %w", key, err)
	}
	size := int64(0)
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	return out.Body, size, nil
}

func (s *S3BlobStore) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("s3 delete %s: %w", key, err)
	}
	return nil
}

func (s *S3BlobStore) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err == nil {
		return true, nil
	}
	if isNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("s3 head %s: %w", key, err)
}

func (s *S3BlobStore) Size(ctx context.Context, key string) (int64, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err != nil {
		if isNotFound(err) {
			return 0, fmt.Errorf("blob not found: %s", key)
		}
		return 0, fmt.Errorf("s3 head %s: %w", key, err)
	}
	if out.ContentLength != nil {
		return *out.ContentLength, nil
	}
	return 0, nil
}

// UsedBytes sums the size of all objects in the bucket.
// This iterates all objects and may be slow on large buckets.
// For production, prefer S3 Storage Lens metrics or a cached counter.
func (s *S3BlobStore) UsedBytes(ctx context.Context) (int64, error) {
	var total int64
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return total, fmt.Errorf("s3 list: %w", err)
		}
		for _, obj := range page.Contents {
			if obj.Size != nil {
				total += *obj.Size
			}
		}
	}
	return total, nil
}

// isNotFound returns true when err represents a 404/NoSuchKey from S3.
func isNotFound(err error) bool {
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var nf *types.NotFound
	if errors.As(err, &nf) {
		return true
	}
	return false
}
