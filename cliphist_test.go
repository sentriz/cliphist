package main

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	mathrand "math/rand"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

func TestMain(m *testing.M) {
	testImage := image.NewRGBA(image.Rectangle{Max: image.Point{20, 20}})

	os.Exit(testscript.RunMain(m, map[string]func() int{
		"cliphist": func() int { main(); return 0 },

		"rand": func() int {
			size, _ := strconv.Atoi(os.Args[1])
			_, _ = io.CopyN(os.Stdout, rand.Reader, int64(size))
			return 0
		},
		"randstr": func() int {
			size, _ := strconv.Atoi(os.Args[1])
			const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
			b := make([]byte, size)
			for i := range b {
				b[i] = letterBytes[mathrand.Intn(len(letterBytes))]
			}
			os.Stdout.Write(b)
			return 0
		},

		"gif":  func() int { _ = gif.Encode(os.Stdout, testImage, nil); return 0 },
		"jpg":  func() int { _ = jpeg.Encode(os.Stdout, testImage, nil); return 0 },
		"png":  func() int { _ = png.Encode(os.Stdout, testImage); return 0 },
		"bmp":  func() int { _ = bmp.Encode(os.Stdout, testImage); return 0 },
		"tiff": func() int { _ = tiff.Encode(os.Stdout, testImage, nil); return 0 },
	}))
}

func TestStoreMaxSize(t *testing.T) {
	tests := []struct {
		name        string
		inputSize   int
		maxStoreSize uint64
		shouldStore bool
	}{
		// Under limit: should store
		{"under limit", 100, 1000, true},
		{"exactly at limit", 1000, 1000, true},

		// Over limit: should not store
		{"over limit by 1", 1001, 1000, false},
		{"way over limit", 5000, 1000, false},

		// maxStoreSize 0 = no limit
		{"no limit small", 100, 0, true},
		{"no limit large", 100000, 0, true},

		// Test with realistic sizes
		{"5MB limit under", 4 * 1024 * 1024, 5 * 1000 * 1000, true},
		{"5MB limit over", 6 * 1024 * 1024, 5 * 1000 * 1000, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary db
			tmpDir, err := os.MkdirTemp("", "cliphist-test-*")
			if err != nil {
				t.Fatalf("failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			dbPath := filepath.Join(tmpDir, "db")

			// Generate input of specified size
			input := make([]byte, tt.inputSize)
			for i := range input {
				input[i] = 'a'
			}

			// Execute store
			err = store(dbPath, bytes.NewReader(input), 100, 750, 0, tt.maxStoreSize)
			if err != nil {
				t.Fatalf("store failed: %v", err)
			}

			// Verify if it was stored
			var output bytes.Buffer
			err = list(dbPath, &output, 100)

			if tt.shouldStore {
				if err != nil {
					t.Fatalf("list failed: %v", err)
				}
				if output.Len() == 0 {
					t.Errorf("expected item to be stored, but list is empty")
				}
			} else {
				// If it shouldn't store, list might fail (db doesn't exist) or return empty
				if err == nil && output.Len() > 0 {
					t.Errorf("expected item NOT to be stored, but list returned: %s", output.String())
				}
			}
		})
	}
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		input    string
		expected uint64
		wantErr  bool
	}{
		// Bytes without unit
		{"0", 0, false},
		{"1024", 1024, false},
		{"5000000", 5000000, false},

		// Decimal units (base 1000)
		{"1KB", 1000, false},
		{"5MB", 5 * 1000 * 1000, false},
		{"1GB", 1000 * 1000 * 1000, false},
		{"2.5MB", 2500000, false},

		// Binary units (base 1024)
		{"1KiB", 1024, false},
		{"5MiB", 5 * 1024 * 1024, false},
		{"1GiB", 1024 * 1024 * 1024, false},
		{"2.5MiB", uint64(2.5 * 1024 * 1024), false},

		// Case insensitive
		{"5mb", 5 * 1000 * 1000, false},
		{"5Mb", 5 * 1000 * 1000, false},
		{"5mib", 5 * 1024 * 1024, false},
		{"5MIB", 5 * 1024 * 1024, false},

		// With spaces
		{" 5MB ", 5 * 1000 * 1000, false},
		{"5 MB", 5 * 1000 * 1000, false},

		// Errors
		{"", 0, true},
		{"abc", 0, true},
		{"MB", 0, true},
		{"-5MB", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := parseSize(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseSize(%q) expected error, got %d", tt.input, result)
				}
				return
			}
			if err != nil {
				t.Errorf("parseSize(%q) unexpected error: %v", tt.input, err)
				return
			}
			if result != tt.expected {
				t.Errorf("parseSize(%q) = %d, want %d", tt.input, result, tt.expected)
			}
		})
	}
}

func TestScripts(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir:                 "testdata",
		RequireExplicitExec: true,
		Setup: func(env *testscript.Env) error {
			env.Vars = append(env.Vars, fmt.Sprintf("HOME=%s", env.WorkDir))
			env.Vars = append(env.Vars, fmt.Sprintf("XDG_CACHE_HOME=%s", env.WorkDir))
			return nil
		},
	})
}
