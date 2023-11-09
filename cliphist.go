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

	bolt "go.etcd.io/bbolt"
)

//go:embed version.txt
var version string

// allow us to test main
func main() { os.Exit(main_()) }
func main_() int {
	flags := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage:\n")
		fmt.Fprintf(os.Stderr, "  $ %s <store|list|decode|delete|delete-query|wipe|version>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "options:\n")
		flags.VisitAll(func(f *flag.Flag) {
			fmt.Fprintf(os.Stderr, "  -%s (default %s)\n", f.Name, f.DefValue)
			fmt.Fprintf(os.Stderr, "    %s\n", f.Usage)
		})
	}

	maxItems := flags.Uint64("max-items", 750, "maximum number of items to store")
	maxDedupeSearch := flags.Uint64("max-dedupe-search", 100, "maximum number of last items to look through when finding duplicates")

	if err := flags.Parse(os.Args[1:]); err != nil {
		return 1
	}

	var err error
	switch flags.Arg(0) {
	case "store":
		switch os.Getenv("CLIPBOARD_STATE") { // from man wl-clipboard
		case "sensitive":
		case "clear":
			err = deleteLast()
		default:
			err = store(os.Stdin, *maxDedupeSearch, *maxItems)
		}
	case "list":
		err = list(os.Stdout)
	case "decode":
		err = decode(os.Stdin, os.Stdout, flags.Arg(1))
	case "delete-query":
		err = deleteQuery(flags.Arg(1))
	case "delete":
		err = delete(os.Stdin)
	case "wipe":
		err = wipe()
	case "version":
		fmt.Fprint(os.Stderr, version)
	default:
		flags.Usage()
		return 1
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func store(in io.Reader, maxDedupeSearch, maxItems uint64) error {
	input, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	if len(input) > 5*1e6 { // don't store >5MB
		return nil
	}

	db, err := initDB()
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

func list(out io.Writer) error {
	db, err := initDBReadOnly()
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
		fmt.Fprintln(out, preview(btoi(k), v))
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

func decode(in io.Reader, out io.Writer, input string) error {
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

	db, err := initDBReadOnly()
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
	if _, err := out.Write(v); err != nil {
		return fmt.Errorf("writing out: %w", err)
	}
	return nil
}

func deleteQuery(query string) error {
	if query == "" {
		return fmt.Errorf("please provide a query")
	}

	db, err := initDB()
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

func deleteLast() error {
	db, err := initDB()
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

func delete(in io.Reader) error {
	input, err := io.ReadAll(in) // drain stdin before opening and locking db
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	db, err := initDB()
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

func wipe() error {
	db, err := initDB()
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

func initDB() (*bolt.DB, error)         { return initDBOption(false) }
func initDBReadOnly() (*bolt.DB, error) { return initDBOption(true) }

func initDBOption(ro bool) (*bolt.DB, error) {
	userCacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("get cache dir: %w", err)
	}
	cacheDir := filepath.Join(userCacheDir, "cliphist")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	dbPath := filepath.Join(cacheDir, "db")

	// https://github.com/etcd-io/bbolt/issues/98
	if ro {
		if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("please store something first")
		}
	}

	db, err := bolt.Open(dbPath, 0644, &bolt.Options{
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

func preview(index uint64, data []byte) string {
	if config, format, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		return fmt.Sprintf("%d%s[[ binary data %s %s %dx%d ]]",
			index, fieldSep, sizeStr(len(data)), format, config.Width, config.Height)
	}
	data = data[:min(len(data), 100)]
	data = bytes.TrimSpace(data)
	data = bytes.Join(bytes.Fields(data), []byte(" "))
	return fmt.Sprintf("%d%s%s", index, fieldSep, data)
}

func min(a, b int) int {
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
