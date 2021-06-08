package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	bolt "go.etcd.io/bbolt"
)

const bucketKey = "b"

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("please provide a command <store|list|decode>")
	}
	switch command := os.Args[1]; command {
	case "store":
		if err := store(); err != nil {
			log.Fatalf("error storing %v", err)
		}
	case "list":
		if err := list(); err != nil {
			log.Fatalf("error listing %v", err)
		}
	case "decode":
		if err := decode(); err != nil {
			log.Fatalf("error decoding %v", err)
		}
	default:
		log.Fatalf("unknown command %q", command)
	}
}

func store() error {
	db, err := initDB(nil)
	if err != nil {
		return fmt.Errorf("creating db: %w", err)
	}
	defer db.Close()
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	if len(input) <= 2 {
		return nil
	}
	if len(bytes.TrimSpace(input)) == 0 {
		return nil
	}
	err = db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketKey))
		c := b.Cursor()
		_, last := c.Last()
		if bytes.Equal(input, last) {
			return nil
		}
		id, _ := b.NextSequence()
		if err := b.Put(itob(id), input); err != nil {
			return fmt.Errorf("insert stdin: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("update db: %w", err)
	}
	return nil
}

func list() error {
	db, err := initDB(&bolt.Options{
		ReadOnly: true,
	})
	if err != nil {
		return fmt.Errorf("creating db: %w", err)
	}
	defer db.Close()
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketKey))
		c := b.Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			fmt.Println(preview(btoi(k), v))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("view db: %w", err)
	}
	return nil
}

var decodeID = regexp.MustCompile(`^(?P<id>\d+)\. `)

func decode() error {
	db, err := initDB(&bolt.Options{
		ReadOnly: true,
	})
	if err != nil {
		return fmt.Errorf("creating db: %w", err)
	}
	defer db.Close()
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	if len(input) <= 2 {
		return fmt.Errorf("input too short to decode")
	}
	matches := decodeID.FindSubmatch(input)
	if len(matches) != 2 {
		return fmt.Errorf("input not prefixed with id")
	}
	idStr := string(matches[1])
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return fmt.Errorf("converting id: %w", err)
	}
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketKey))
		v := b.Get(itob(uint64(id)))
		os.Stdout.Write(v)
		return nil
	})
	if err != nil {
		return fmt.Errorf("view db: %w", err)
	}
	return nil
}

func initDB(opts *bolt.Options) (*bolt.DB, error) {
	userCacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("get cache dir: %w", err)
	}
	cacheDir := filepath.Join(userCacheDir, "cliphist")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	dbPath := filepath.Join(cacheDir, "db")
	db, err := bolt.Open(dbPath, 0600, opts)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(bucketKey))
		return err
	})
	if err != nil && !errors.Is(err, bolt.ErrDatabaseReadOnly) {
		return nil, fmt.Errorf("init bucket: %w", err)
	}
	return db, nil
}

func preview(index uint64, data []byte) string {
	data = data[:min(len(data), 100)]
	if mime := mime(data); mime != "" {
		return fmt.Sprintf("%d. binary data %s", index, mime)
	}
	data = bytes.TrimSpace(data)
	data = bytes.Join(bytes.Fields(data), []byte(" "))
	return fmt.Sprintf("%d. %s", index, data)
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
