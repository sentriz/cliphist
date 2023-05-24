package main

import (
	"crypto/rand"
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
			_ = png.Encode(os.Stdout, image.NewRGBA(image.Rectangle{Max: image.Point{20, 20}}))
			return 0
		},
		"jpg": func() int {
			_ = jpeg.Encode(os.Stdout, image.NewRGBA(image.Rectangle{Max: image.Point{20, 20}}), nil)
			return 0
		},
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
