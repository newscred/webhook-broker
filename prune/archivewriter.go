package prune

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"gocloud.dev/blob"
	_ "gocloud.dev/blob/fileblob"
	_ "gocloud.dev/blob/gcsblob"
	_ "gocloud.dev/blob/s3blob"
)

// Bucket defines the interface for cloud storage operations.
type Bucket interface {
	NewReader(ctx context.Context, key string, opts *blob.ReaderOptions) (Reader, error)
	NewWriter(ctx context.Context, key string, opts *blob.WriterOptions) (Writer, error)
	Copy(ctx context.Context, dstKey, srcKey string, opts *blob.CopyOptions) error
	Exists(ctx context.Context, key string) (bool, error)
}

// Reader defines the interface for reading from cloud storage objects.
type Reader interface {
	io.ReadCloser
	Size() int64
}

// Writer defines the interface for writing to cloud storage objects.
type Writer interface {
	io.WriteCloser
}

// blobBucket implements the Bucket interface using "gocloud.dev/blob".
type blobBucket struct {
	*blob.Bucket
}

// NewReader creates a new Reader for the given object key.
func (b *blobBucket) NewReader(ctx context.Context, key string, opts *blob.ReaderOptions) (Reader, error) {
	return b.Bucket.NewReader(ctx, key, opts)
}

// NewWriter creates a new Writer for the given object key.
func (b *blobBucket) NewWriter(ctx context.Context, key string, opts *blob.WriterOptions) (Writer, error) {
	return b.Bucket.NewWriter(ctx, key, opts)
}

// NewBlobBucket creates a new Bucket using "gocloud.dev/blob".
func NewBlobBucket(bucket *blob.Bucket) Bucket {
	return &blobBucket{bucket}
}

// ArchiveWriteManager handles file rotation for cloud storage.
type ArchiveWriteManager struct {
	bucket      Bucket
	objectName  string
	maxSize     int64
	currentSize int64
	mu          sync.Mutex
	writer      Writer
}

// NewArchiveWriteManager creates a new RotationManager.
func NewArchiveWriteManager(bucket Bucket, objectName string, maxSize int64) (*ArchiveWriteManager, error) {
	rm := &ArchiveWriteManager{
		bucket:     bucket,
		objectName: objectName,
		maxSize:    maxSize,
	}

	// Initialize currentSize using reader
	ctx := context.Background()
	r, err := bucket.NewReader(ctx, objectName, nil)
	exists, exists_err := bucket.Exists(ctx, objectName)
	if exists_err != nil {
		return nil, fmt.Errorf("failed to check if object exists: %w", exists_err)
	}
	if err != nil && exists {
		return nil, fmt.Errorf("failed to create reader: %w", err)
	}
	if err == nil {
		defer r.Close()
		rm.currentSize = r.Size()
	}

	return rm, nil
}

// Write writes the given JSON string to the current object.
// It handles rotation if the maximum size is exceeded.
func (rm *ArchiveWriteManager) Write(ctx context.Context, jsonStr string) (int, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if rm.writer == nil {
		// If no writer exists, create a new one
		var err error
		rm.writer, err = rm.bucket.NewWriter(ctx, rm.objectName, nil)
		if err != nil {
			return 0, fmt.Errorf("failed to create writer: %w", err)
		}
		rm.currentSize = 0 // Reset size for the new object
	}

	n, err := rm.writer.Write([]byte(jsonStr))
	if err != nil {
		return n, fmt.Errorf("failed to write to object: %w", err)
	}
	rm.currentSize += int64(n)

	if rm.currentSize >= rm.maxSize {
		// Initiate rotation in the background
		go rm.rotateInBackground(ctx)
	}

	return n, nil
}

// rotateInBackground performs the object rotation in a separate goroutine.
func (rm *ArchiveWriteManager) rotateInBackground(ctx context.Context) {
	rm.mu.Lock() // Lock to prevent concurrent rotations
	defer rm.mu.Unlock()

	// Close the current writer before rotating
	if rm.writer != nil {
		if err := rm.writer.Close(); err != nil {
			log.Error().Err(err).Msg("failed to close writer")
			return
		}
		rm.writer = nil
	}

	if err := rm.rotate(ctx); err != nil {
		log.Error().Err(err).Msg("failed to rotate object")
	}
}

// rotate performs the actual object rotation.
func (rm *ArchiveWriteManager) rotate(ctx context.Context) error {
	// Extract file extension
	fileExt := filepath.Ext(rm.objectName)
	baseName := rm.objectName[0 : len(rm.objectName)-len(fileExt)]

	// Create new object name with timestamp before extension
	newObjectName := fmt.Sprintf("%s_%d%s", baseName, time.Now().Unix(), fileExt)
	if err := rm.bucket.Copy(ctx, newObjectName, rm.objectName, nil); err != nil {
		return fmt.Errorf("failed to copy object: %w", err)
	}

	return nil
}

// Close closes the underlying writer.
func (rm *ArchiveWriteManager) Close() error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.writer != nil {
		return rm.writer.Close()
	}
	return nil
}
