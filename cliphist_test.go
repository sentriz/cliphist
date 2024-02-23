package main

import (
	"crypto/rand"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	mathrand "math/rand"
	"os"
	"strconv"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
	"golang.org/x/image/bmp"
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

		"gif": func() int { _ = gif.Encode(os.Stdout, testImage, nil); return 0 },
		"jpg": func() int { _ = jpeg.Encode(os.Stdout, testImage, nil); return 0 },
		"png": func() int { _ = png.Encode(os.Stdout, testImage); return 0 },
		"bmp": func() int { _ = bmp.Encode(os.Stdout, testImage); return 0 },
	}))
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
