package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "embed"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"github.com/rivo/uniseg"
	bolt "go.etcd.io/bbolt"
	"go.senan.xyz/flagconf"
)

//go:embed version.txt
var version string

//nolint:errcheck
func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  %s <command> [arguments]\n", flag.CommandLine.Name())
		fmt.Fprintf(flag.CommandLine.Output(), "\nCommands:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  store           read clipboard content from stdin and store it (deduplicates, respects size/length limits)\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  list            list all stored clipboard items with previews\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  decode [id]     decode and print the full content for the given ID (or read ID from stdin)\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  delete-query    delete all entries containing a substring (query argument required)\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  delete          delete entries by reading IDs from stdin (one per line)\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  wipe            remove all entries and compact the database\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  compact         compact the database to reclaim space without deleting entries\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  version         print version and current flag values\n")

		fmt.Fprintf(flag.CommandLine.Output(), "\nOptions:\n")
		flag.VisitAll(func(f *flag.Flag) {
			fmt.Fprintf(flag.CommandLine.Output(), "  -%s (default %s)\n", f.Name, f.DefValue)
			fmt.Fprintf(flag.CommandLine.Output(), "    %s\n", f.Usage)
		})
	}

	cacheHome, err := os.UserCacheDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	configHome, err := os.UserConfigDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	maxItems := flag.Uint64("max-items", 750, "maximum number of items to store")
	maxDedupeSearch := flag.Uint64("max-dedupe-search", 100, "maximum number of last items to look through when finding duplicates")
	minLength := flag.Uint("min-store-length", 0, "minimum number of characters to store")
	previewWidth := flag.Uint("preview-width", 100, "maximum number of characters to preview")
	maxStoreSizeStr := flag.String("max-store-size", "5MB", "maximum size of clipboard to store (e.g., 5MB, 10MiB, 1GB)")
	dbPath := flag.String("db-path", filepath.Join(cacheHome, "cliphist", "db"), "path to db")
	configPath := flag.String("config-path", filepath.Join(configHome, "cliphist", "config"), "overwrite config path to use instead of cli flags")

	flag.Parse()
	flagconf.ParseEnv()
	flagconf.ParseConfig(*configPath)

	maxStoreSize, err := parseSize(*maxStoreSizeStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid max-store-size value: %v\n", err)
		os.Exit(1)
	}

	switch flag.Arg(0) {
	case "store":
		switch os.Getenv("CLIPBOARD_STATE") { // from man wl-clipboard
		case "sensitive":
		case "clear":
			err = deleteLast(*dbPath)
		default:
			err = store(*dbPath, os.Stdin, *maxDedupeSearch, *maxItems, *minLength, maxStoreSize)
		}
	case "list":
		err = list(*dbPath, os.Stdout, *previewWidth)
	case "decode":
		err = decode(*dbPath, os.Stdin, os.Stdout, flag.Arg(1))
	case "delete-query":
		err = deleteQuery(*dbPath, flag.Arg(1))
	case "delete":
		err = delete(*dbPath, os.Stdin)
	case "wipe":
		err = wipeAndCompact(*dbPath)
	case "compact":
		err = compactDB(*dbPath)
	case "version":
		fmt.Fprintf(flag.CommandLine.Output(), "%s\t%s\n", "version", strings.TrimSpace(version))
		flag.VisitAll(func(f *flag.Flag) {
			fmt.Fprintf(flag.CommandLine.Output(), "%s\t%s\n", f.Name, f.Value)
		})
	default:
		flag.Usage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func store(dbPath string, in io.Reader, maxDedupeSearch, maxItems uint64, minLength uint, maxStoreSize uint64) error {
	input, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	if maxStoreSize > 0 && uint64(len(input)) > maxStoreSize {
		return nil
	}
	if int(minLength) > 0 && graphemeClusterCount(string(input)) < int(minLength) {
		return nil
	}

	db, err := initDB(dbPath)
	if err != nil {
		return fmt.Errorf("opening db: %w", err)
	}
	defer db.Close()

	if len(bytes.TrimSpace(input)) == 0 {
		return nil
	}
	tx, err := db.Begin(true)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	b := tx.Bucket([]byte(bucketKey))

	if err := deduplicate(b, input, maxDedupeSearch); err != nil {
		return fmt.Errorf("deduplicating: %w", err)
	}
	id, err := b.NextSequence()
	if err != nil {
		return fmt.Errorf("getting next sequence: %w", err)
	}
	if err := b.Put(itob(id), input); err != nil {
		return fmt.Errorf("insert stdin: %w", err)
	}
	if err := trimLength(b, maxItems); err != nil {
		return fmt.Errorf("trimming length: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// trim the store's size to a number of max items. manually counting
// seen items because we can't rely on sequence numbers when items can
// be deleted when deduplicating
func trimLength(b *bolt.Bucket, maxItems uint64) error {
	c := b.Cursor()
	var seen uint64
	for k, _ := c.Last(); k != nil; k, _ = c.Prev() {
		if seen < maxItems {
			seen++
			continue
		}
		if err := b.Delete(k); err != nil {
			return fmt.Errorf("delete :%w", err)
		}
		seen++
	}
	return nil
}

func deduplicate(b *bolt.Bucket, input []byte, maxDedupeSearch uint64) error {
	c := b.Cursor()
	var seen uint64
	for k, v := c.Last(); k != nil; k, v = c.Prev() {
		if seen > maxDedupeSearch {
			break
		}
		if !bytes.Equal(v, input) {
			seen++
			continue
		}
		if err := b.Delete(k); err != nil {
			return fmt.Errorf("delete :%w", err)
		}
		seen++
	}
	return nil
}

func list(dbPath string, out io.Writer, previewWidth uint) error {
	db, err := initDBReadOnly(dbPath)
	if err != nil {
		return fmt.Errorf("opening db: %w", err)
	}
	defer db.Close()

	tx, err := db.Begin(false)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	b := tx.Bucket([]byte(bucketKey))
	c := b.Cursor()
	for k, v := c.Last(); k != nil; k, v = c.Prev() {
		fmt.Fprintln(out, preview(btoi(k), v, previewWidth))
	}
	return nil
}

const fieldSep = "\t"

func extractID(input string) (uint64, error) {
	idStr, _, _ := strings.Cut(input, fieldSep)
	if idStr == "" {
		return 0, fmt.Errorf("input not prefixed with id")
	}
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return 0, fmt.Errorf("converting id: %w", err)
	}
	return uint64(id), nil
}

func decode(dbPath string, in io.Reader, out io.Writer, input string) error {
	if input == "" {
		inp, err := io.ReadAll(in)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		input = string(inp)
	}
	id, err := extractID(input)
	if err != nil {
		return fmt.Errorf("extracting id: %w", err)
	}

	db, err := initDBReadOnly(dbPath)
	if err != nil {
		return fmt.Errorf("opening db: %w", err)
	}
	defer db.Close()

	tx, err := db.Begin(false)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	b := tx.Bucket([]byte(bucketKey))
	v := b.Get(itob(id))
	if v == nil {
		return fmt.Errorf("id %d not found", id)
	}

	if _, err := out.Write(v); err != nil {
		return fmt.Errorf("writing out: %w", err)
	}
	return nil
}

func deleteQuery(dbPath string, query string) error {
	if query == "" {
		return fmt.Errorf("please provide a query")
	}

	db, err := initDB(dbPath)
	if err != nil {
		return fmt.Errorf("opening db: %w", err)
	}
	defer db.Close()

	tx, err := db.Begin(true)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	b := tx.Bucket([]byte(bucketKey))
	c := b.Cursor()
	for k, v := c.First(); k != nil; k, v = c.Next() {
		if bytes.Contains(v, []byte(query)) {
			_ = b.Delete(k)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func deleteLast(dbPath string) error {
	db, err := initDB(dbPath)
	if err != nil {
		return fmt.Errorf("opening db: %w", err)
	}
	defer db.Close()

	tx, err := db.Begin(true)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	b := tx.Bucket([]byte(bucketKey))
	c := b.Cursor()
	k, _ := c.Last()
	_ = b.Delete(k)

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func delete(dbPath string, in io.Reader) error {
	input, err := io.ReadAll(in) // drain stdin before opening and locking db
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	db, err := initDB(dbPath)
	if err != nil {
		return fmt.Errorf("opening db: %w", err)
	}
	defer db.Close()

	tx, err := db.Begin(true)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	for sc := bufio.NewScanner(bytes.NewReader(input)); sc.Scan(); {
		id, err := extractID(sc.Text())
		if err != nil {
			return fmt.Errorf("extract id: %w", err)
		}
		b := tx.Bucket([]byte(bucketKey))
		if err := b.Delete(itob(id)); err != nil {
			return fmt.Errorf("delete key: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func wipeAndCompact(dbPath string) error {
	if err := wipe(dbPath); err != nil {
		return fmt.Errorf("wipe: %w", err)
	}
	if err := compactDB(dbPath); err != nil {
		return fmt.Errorf("compact: %w", err)
	}
	return nil
}

func wipe(dbPath string) error {
	db, err := initDB(dbPath)
	if err != nil {
		return fmt.Errorf("opening db: %w", err)
	}
	defer db.Close()

	tx, err := db.Begin(true)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	b := tx.Bucket([]byte(bucketKey))
	c := b.Cursor()
	for k, _ := c.First(); k != nil; k, _ = c.Next() {
		_ = b.Delete(k)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

const bucketKey = "b"

func initDB(path string) (*bolt.DB, error)         { return initDBOption(path, false) }
func initDBReadOnly(path string) (*bolt.DB, error) { return initDBOption(path, true) }

func initDBOption(path string, ro bool) (*bolt.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}

	// https://github.com/etcd-io/bbolt/issues/98
	if ro {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("please store something first")
		}
	}

	db, err := bolt.Open(path, 0644, &bolt.Options{
		ReadOnly: ro,
		Timeout:  1 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if db.IsReadOnly() {
		return db, nil
	}
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(bucketKey))
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("init bucket: %w", err)
	}
	return db, nil
}

func compactDB(path string) error {
	srcDB, err := bolt.Open(path, 0644, &bolt.Options{
		ReadOnly: true,
		Timeout:  1 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("open source db: %w", err)
	}
	defer srcDB.Close()

	tmpPath := path + ".tmp"
	dstDB, err := bolt.Open(tmpPath, 0644, &bolt.Options{
		Timeout: 1 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("open destination db: %w", err)
	}
	defer dstDB.Close()

	if err := bolt.Compact(dstDB, srcDB, 0); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("compact db: %w", err)
	}

	if err := srcDB.Close(); err != nil {
		return fmt.Errorf("close source db: %w", err)
	}
	if err := dstDB.Close(); err != nil {
		return fmt.Errorf("close destination db: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace db: %w", err)
	}
	return nil
}

func preview(index uint64, data []byte, width uint) string {
	if config, format, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		return fmt.Sprintf("%d%s[[ binary data %s %s %dx%d ]]",
			index, fieldSep, sizeStr(len(data)), format, config.Width, config.Height)
	}
	prev := string(data)
	prev = strings.TrimSpace(prev)
	prev = strings.Join(strings.Fields(prev), " ")
	prev = trunc(prev, int(width), "…")
	return fmt.Sprintf("%d%s%s", index, fieldSep, prev)
}

func trunc(in string, max int, ellip string) string {
	runes := []rune(in)
	if len(runes) > max {
		return string(runes[:max]) + ellip
	}
	return in
}

func graphemeClusterCount(str string) int {
	return uniseg.GraphemeClusterCount(str)
}

func min(a, b int) int { //nolint:unused // we still support go1.19
	if a < b {
		return a
	}
	return b
}

func itob(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

func btoi(v []byte) uint64 {
	return binary.BigEndian.Uint64(v)
}

func sizeStr(size int) string {
	units := []string{"B", "KiB", "MiB"}

	var i int
	fsize := float64(size)
	for fsize >= 1024 && i < len(units)-1 {
		fsize /= 1024
		i++
	}
	return fmt.Sprintf("%.0f %s", fsize, units[i])
}

// parseSize parses a size string like "5MB", "10MiB", "1024" into bytes
func parseSize(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size string")
	}

	// Ordered from longest to shortest suffix to avoid partial matches
	units := []struct {
		suffix     string
		multiplier uint64
	}{
		{"gib", 1024 * 1024 * 1024},
		{"mib", 1024 * 1024},
		{"kib", 1024},
		{"gb", 1000 * 1000 * 1000},
		{"mb", 1000 * 1000},
		{"kb", 1000},
		{"b", 1},
	}

	lower := strings.ToLower(s)

	for _, unit := range units {
		if strings.HasSuffix(lower, unit.suffix) {
			numStr := s[:len(s)-len(unit.suffix)]
			numStr = strings.TrimSpace(numStr)
			num, err := strconv.ParseFloat(numStr, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid number: %w", err)
			}
			if num < 0 {
				return 0, fmt.Errorf("size cannot be negative")
			}
			return uint64(num * float64(unit.multiplier)), nil
		}
	}

	// No unit suffix, treat as bytes
	num, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size: %w", err)
	}
	return num, nil
}
