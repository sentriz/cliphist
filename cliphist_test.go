package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"strconv"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

func TestMain(m *testing.M) {
	os.Exit(testscript.RunMain(m, map[string]func() int{
		"cliphist": main_,
		"rand": func() int {
			size, _ := strconv.Atoi(os.Args[1])
			_, _ = io.CopyN(os.Stdout, rand.Reader, int64(size))
			return 0
		},
		"png": func() int {
			_ = png.Encode(os.Stdout, image.NewRGBA(image.Rectangle{
				Min: image.Point{0, 0},
				Max: image.Point{20, 20},
			}))
			return 0
		},
		"jpg": func() int {
			_ = jpeg.Encode(os.Stdout, image.NewRGBA(image.Rectangle{
				Min: image.Point{0, 0},
				Max: image.Point{20, 20},
			}), nil)
			return 0
		},
	}))
}

func TestScripts(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: "testdata",
		Setup: func(env *testscript.Env) error {
			env.Vars = append(env.Vars, fmt.Sprintf("HOME=%s", env.WorkDir))
			env.Vars = append(env.Vars, fmt.Sprintf("XDG_CACHE_HOME=%s", env.WorkDir))
			return nil
		},
	})
}

func FuzzStoreList(f *testing.F) {
	home := f.TempDir()
	f.Setenv("HOME", home)
	f.Setenv("XDG_CACHE_HOME", home)

	f.Fuzz(func(t *testing.T, in []byte) {
		if len(bytes.TrimSpace(in)) == 0 {
			return
		}

		fail := func(f string, a ...any) {
			t.Fatalf("input %s: %s", base64.StdEncoding.EncodeToString(in), fmt.Sprintf(f, a...))
		}

		if err := store(bytes.NewReader(in), 0, 5); err != nil {
			fail("store: %v", err)
		}

		var previewBuff bytes.Buffer
		if err := list(&previewBuff); err != nil {
			fail("list: %v", err)
		}

		previewScanner := bufio.NewScanner(&previewBuff)
		previewScanner.Scan()

		firstLine := previewScanner.Bytes()
		if len(firstLine) == 0 {
			fail("no line")
		}

		var out bytes.Buffer
		if err := decode(bytes.NewReader(firstLine), &out); err != nil {
			fail("decode: %v", string(firstLine), err)
		}

		if !bytes.Equal(in, out.Bytes()) {
			fail("not equal")
		}
	})
}
