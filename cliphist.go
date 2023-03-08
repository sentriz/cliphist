package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	maxItems        = flag.Uint64("max-items", 750, "maximum number of items to store")
	maxDedupeSearch = flag.Uint64("max-dedupe-search", 20, "maximum number of last items to look through when finding duplicates")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage:\n")
		fmt.Fprintf(os.Stderr, "  %s <%s>\n", os.Args[0], strings.Join(commandList, "|"))
		fmt.Fprintf(os.Stderr, "options:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if len(flag.Args()) == 0 {
		flag.Usage()
		os.Exit(1)
	}
	cmd, ok := commands[flag.Args()[0]]
	if !ok {
		flag.Usage()
		os.Exit(1)
	}
	if err := cmd(flag.Args()[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error in %q: %v", flag.Args()[0], err)
		os.Exit(1)
	}
}

var commands = map[string]func(args []string) error{
	"store": func(_ []string) error {
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}

		db, err := initDB()
		if err != nil {
			return fmt.Errorf("opening db: %v", err)
		}
		defer db.Close()

		if err := store(db, input, *maxDedupeSearch, *maxItems); err != nil {
			return fmt.Errorf("storing: %w", err)
		}
		return nil
	},

	"list": func(_ []string) error {
		db, err := initDBReadOnly()
		if err != nil {
			return fmt.Errorf("opening db: %w", err)
		}
		defer db.Close()
		if err := list(db, os.Stdout); err != nil {
			return fmt.Errorf("listing: %w", err)
		}
		return nil
	},

	"decode": func(_ []string) error {
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		db, err := initDBReadOnly()
		if err != nil {
			return fmt.Errorf("opening db: %w", err)
		}
		defer db.Close()
		if err := decode(db, input, os.Stdout); err != nil {
			return fmt.Errorf("decoding: %w", err)
		}
		return nil
	},

	"delete-query": func(args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("no query provided")
		}
		db, err := initDB()
		if err != nil {
			return fmt.Errorf("opening db: %w", err)
		}
		defer db.Close()
		if err := deleteQuery(db, args[0]); err != nil {
			return fmt.Errorf("deleting query: %w", err)
		}
		return nil
	},

	"delete": func(args []string) error {
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		db, err := initDB()
		if err != nil {
			return fmt.Errorf("opening db: %w", err)
		}
		defer db.Close()
		if err := delete(db, input); err != nil {
			return fmt.Errorf("deleting query: %w", err)
		}
		return nil
	},

	"wipe": func(_ []string) error {
		db, err := initDB()
		if err != nil {
			return fmt.Errorf("opening db: %w", err)
		}
		defer db.Close()
		if err := wipe(db); err != nil {
			return fmt.Errorf("wiping: %w", err)
		}
		return nil
	},
}

var commandList []string

func init() {
	for command := range commands {
		commandList = append(commandList, command)
	}
}

func store(db *bolt.DB, input []byte, maxDedupeSearch, maxItems uint64) error {
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

func list(db *bolt.DB, out io.Writer) error {
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

func extractID(input []byte) (uint64, error) {
	idStr, _, found := strings.Cut(string(input), "\t")
	if !found {
		return 0, fmt.Errorf("input not prefixed with id")
	}
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return 0, fmt.Errorf("converting id: %w", err)
	}
	return uint64(id), nil
}

func decode(db *bolt.DB, input []byte, out io.Writer) error {
	id, err := extractID(input)
	if err != nil {
		return fmt.Errorf("extracting id: %w", err)
	}

	tx, err := db.Begin(false)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	b := tx.Bucket([]byte(bucketKey))
	v := b.Get(itob(uint64(id)))
	if _, err := out.Write(v); err != nil {
		return fmt.Errorf("writing out: %w", err)
	}
	return nil
}

func deleteQuery(db *bolt.DB, query string) error {
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

func delete(db *bolt.DB, input []byte) error {
	id, err := extractID(input)
	if err != nil {
		return fmt.Errorf("extract id: %w", err)
	}

	tx, err := db.Begin(true)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	b := tx.Bucket([]byte(bucketKey))
	if err := b.Delete(itob(uint64(id))); err != nil {
		return fmt.Errorf("delete key: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func wipe(db *bolt.DB) error {
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
	data = data[:min(len(data), 100)]
	if mime := mime(data); mime != "" {
		return fmt.Sprintf("%d\tbinary data %s", index, mime)
	}
	data = bytes.TrimSpace(data)
	data = bytes.Join(bytes.Fields(data), []byte(" "))
	return fmt.Sprintf("%d\t%s", index, data)
}

func mime(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\x0D\x0A\x1A\x0A")):
		return "image/png"
	case bytes.HasPrefix(data, []byte("\xFF\xD8\xFF")):
		return "image/jpeg"
	case bytes.HasPrefix(data, []byte("GIF87a")):
		return "image/gif"
	case bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif"
	default:
		return ""
	}
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
