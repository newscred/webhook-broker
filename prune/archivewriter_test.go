package prune

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"regexp"
	"sync"
	"testing"

	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"
)

// errorWriter is a Writer implementation that always returns an error on Write.
type errorWriter struct{}

// Write always returns an error and 0 bytes written.
func (e *errorWriter) Write(p []byte) (n int, err error) {
	return 0, errors.New("error on write")
}

// Close does nothing and returns nil.
func (e *errorWriter) Close() error {
	return nil
}

type errorBucket struct {
	Bucket
	errOnNewWriter bool
	errOnCopy      bool
	errOnExists    bool
	errOnWrite     bool
}

func (e errorBucket) NewWriter(ctx context.Context, key string, opts *blob.WriterOptions) (Writer, error) {
	if e.errOnNewWriter {
		return nil, errors.New("error on new writer")
	}
	if e.errOnWrite {
		return &errorWriter{}, nil // Return erroring writer
	}
	return e.Bucket.NewWriter(ctx, key, opts)
}

func (e errorBucket) Copy(ctx context.Context, dstKey, srcKey string, opts *blob.CopyOptions) error {
	if e.errOnCopy {
		return errors.New("error on copy")
	}
	return e.Bucket.Copy(ctx, dstKey, srcKey, opts)
}

func (e errorBucket) Exists(ctx context.Context, key string) (bool, error) {
	if e.errOnExists {
		return false, errors.New("error on exists")
	}
	return e.Bucket.Exists(ctx, key)
}

func TestArchiveWriteManager(t *testing.T) {
	t.Parallel()

	getBothBucket := func() (Bucket, *blob.Bucket) {
		memBucket := memblob.OpenBucket(nil)
		return NewBlobBucket(memBucket), memBucket
	}

	getBucket := func() Bucket {
		bucket, _ := getBothBucket()
		return bucket
	}

	objectName := "test_archive.jsonl"
	maxSize := int64(1024) // 1 KB for testing

	t.Run("Success", func(t *testing.T) {
		t.Parallel() // Add t.Parallel() here
		bucket, memBucket := getBothBucket()
		rm, err := NewArchiveWriteManager(bucket, objectName, maxSize)
		if err != nil {
			t.Fatal(err)
		}
		defer rm.Close()

		// Generate random JSON strings
		jsonData := generateRandomJSON(maxSize/2, 10) // Half the maxSize, 10 lines

		// Write data in parallel
		var wg sync.WaitGroup
		for _, jsonStr := range jsonData {
			wg.Add(1)
			go func(str string) {
				defer wg.Done()
				_, err := rm.Write(context.Background(), str)
				if err != nil {
					t.Errorf("Write error: %v", err)
				}
			}(jsonStr)
		}
		wg.Wait()

		// Check if rotation happened
		ctx := context.Background()
		exists, err := bucket.Exists(ctx, objectName)
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Error("Original object does not exist")
		}

		// Check if a rotated object exists
		iter := memBucket.List(nil)
		rotatedObjectFound := false
		regex, err := regexp.Compile(`test_archive_[0-9]+\.jsonl`)
		if err != nil {
			t.Fatal(err)
		}
		for {
			obj, err := iter.Next(ctx)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if regex.MatchString(obj.Key) {
				rotatedObjectFound = true
				break
			}
		}
		if !rotatedObjectFound {
			t.Error("Rotated object not found")
		}
	})

	t.Run("ExistingObject", func(t *testing.T) {
		t.Parallel()

		// Use a different object name for this test case
		existingObjectName := "existing_object.jsonl"

		// Create an existing object in the bucket
		ctx := context.Background()
		bucket := getBucket()
		existingWriter, err := bucket.NewWriter(ctx, existingObjectName, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = existingWriter.Write([]byte("existing content"))
		if err != nil {
			t.Fatal(err)
		}
		if err := existingWriter.Close(); err != nil {
			t.Fatal(err)
		}

		rm, err := NewArchiveWriteManager(bucket, existingObjectName, maxSize)
		if err != nil {
			t.Fatal(err)
		}
		defer rm.Close()

		// Verify that currentSize is initialized correctly
		if rm.currentSize != 16 { // Size of "existing content"
			t.Errorf("Expected currentSize to be 16, got %d", rm.currentSize)
		}
	})

	t.Run("WriteError", func(t *testing.T) {
		t.Parallel() // Add t.Parallel() here
		rm, err := NewArchiveWriteManager(errorBucket{Bucket: getBucket(), errOnWrite: true}, objectName, maxSize)
		if err != nil {
			t.Fatal(err)
		}
		defer rm.Close()

		// Generate random JSON strings
		jsonData := generateRandomJSON(maxSize/2, 10) // Half the maxSize, 10 lines
		rm.Close()
		// Write data in parallel
		var wg sync.WaitGroup
		for _, jsonStr := range jsonData {
			wg.Add(1)
			go func(str string) {
				defer wg.Done()
				_, err := rm.Write(context.Background(), str)
				if err == nil {
					t.Errorf("Write should have failed")
				}
			}(jsonStr)
		}
		wg.Wait()
	})

	t.Run("NewWriterError", func(t *testing.T) {
		t.Parallel() // Add t.Parallel() here
		rm, err := NewArchiveWriteManager(errorBucket{Bucket: getBucket(), errOnNewWriter: true}, objectName, maxSize)
		if err != nil {
			t.Fatal(err)
		}
		defer rm.Close()

		// Generate random JSON strings
		jsonData := generateRandomJSON(maxSize/2, 10) // Half the maxSize, 10 lines

		// Write data in parallel
		var wg sync.WaitGroup
		for _, jsonStr := range jsonData {
			wg.Add(1)
			go func(str string) {
				defer wg.Done()
				_, err := rm.Write(context.Background(), str)
				if err == nil {
					t.Errorf("Write should have failed")
				}
			}(jsonStr)
		}
		wg.Wait()
	})

	t.Run("RotateError", func(t *testing.T) {
		t.Parallel() // Add t.Parallel() here
		rm, err := NewArchiveWriteManager(errorBucket{Bucket: getBucket(), errOnCopy: true}, objectName, maxSize)
		if err != nil {
			t.Fatal(err)
		}
		defer rm.Close()

		// Generate random JSON strings
		jsonData := generateRandomJSON(maxSize, 10) // Half the maxSize, 10 lines

		// Write data in parallel
		var wg sync.WaitGroup
		for _, jsonStr := range jsonData {
			wg.Add(1)
			go func(str string) {
				defer wg.Done()
				_, err := rm.Write(context.Background(), str)
				if err != nil {
					t.Errorf("Write error: %v", err)
				}
			}(jsonStr)
		}
		wg.Wait()
	})

	t.Run("ExistsError", func(t *testing.T) {
		t.Parallel() // Add t.Parallel() here
		_, err := NewArchiveWriteManager(errorBucket{Bucket: getBucket(), errOnExists: true}, objectName, maxSize)
		if err == nil {
			t.Fatal("Expected error for bucket.Exists, got nil")
		}
	})
}

func generateRandomJSON(size int64, numLines int) []string {
	jsonData := make([]string, numLines)
	for i := 0; i < numLines; i++ {
		str := make([]rune, size/2)
		for j := range str {
			str[j] = rune(rand.Intn(0x10000))
		}
		jsonData[i] = fmt.Sprintf(`{"line": "%s"}`, string(str))
	}
	return jsonData
}
