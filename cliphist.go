package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
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
		fmt.Fprintf(flag.CommandLine.Output(), "  $ %s <store|list|decode|delete|delete-query|wipe|compact|version>\n", flag.CommandLine.Name())
		fmt.Fprintf(flag.CommandLine.Output(), "  $ %s [options] list [-fields <fields>] [id]\n", flag.CommandLine.Name())
		fmt.Fprintf(flag.CommandLine.Output(), "  $ %s [options] wipe [-older-than <duration>]\n", flag.CommandLine.Name())
		fmt.Fprintf(flag.CommandLine.Output(), "\nOptions:\n")
		flag.PrintDefaults()
		fmt.Fprintf(flag.CommandLine.Output(), "\nSee also:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  $ %s list -h\n", flag.CommandLine.Name())
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
	maxStoreSize := uint64(5 * 1000 * 1000)
	flag.Var(sizeParser{&maxStoreSize}, "max-store-size", "maximum size of clipboard to store (e.g., 5MB, 10MiB, 1GB)")
	dbPath := flag.String("db-path", filepath.Join(cacheHome, "cliphist", "db"), "path to db")
	configPath := flag.String("config-path", filepath.Join(configHome, "cliphist", "config"), "overwrite config path to use instead of cli flags")

	flag.Parse()
	flagconf.ParseEnv()
	flagconf.ParseConfig(*configPath)

	usage := func() {
		flag.Usage()
		os.Exit(1)
	}

	var id, query string
	var listArgs, wipeArgs []string

	switch args := flag.Args(); {
	case match(args, "store"):
		switch os.Getenv("CLIPBOARD_STATE") { // from man wl-clipboard
		case "sensitive":
		case "clear":
			err = deleteLast(*dbPath)
		default:
			err = store(*dbPath, os.Stdin, *maxDedupeSearch, *maxItems, *minLength, maxStoreSize)
		}
	case match(args, "list", &listArgs):
		flag := flag.NewFlagSet("list", flag.ExitOnError)
		fields := flag.String("fields", "id,preview", "comma separated fields (id, preview, timestamp, mime) keep id first for decode or delete")
		flag.Parse(listArgs)
		switch rest := flag.Args(); {
		case match(rest), match(rest, &id):
			err = list(*dbPath, os.Stdout, *previewWidth, strings.Split(*fields, ","), id)
		default:
			usage()
		}
	case match(args, "decode"):
		err = decode(*dbPath, os.Stdin, os.Stdout, "")
	case match(args, "decode", &id):
		err = decode(*dbPath, os.Stdin, os.Stdout, id)
	case match(args, "delete-query", &query):
		err = deleteQuery(*dbPath, query)
	case match(args, "delete"):
		err = delete(*dbPath, os.Stdin)
	case match(args, "wipe", &wipeArgs):
		flag := flag.NewFlagSet("wipe", flag.ExitOnError)
		olderThan := flag.Duration("older-than", 0, "only wipe entries older than this duration (eg. 1h, 720h)")
		flag.Parse(wipeArgs)
		switch rest := flag.Args(); {
		case match(rest):
			err = wipeAndCompact(*dbPath, *olderThan)
		default:
			usage()
		}
	case match(args, "compact"):
		err = compactDB(*dbPath)
	case match(args, "version"):
		fmt.Fprintf(flag.CommandLine.Output(), "%s\t%s\n", "version", strings.TrimSpace(version))
		flag.VisitAll(func(f *flag.Flag) {
			fmt.Fprintf(flag.CommandLine.Output(), "%s\t%s\n", f.Name, f.Value)
		})
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func match(args []string, pattern ...any) bool {
	for i, p := range pattern {
		switch p := p.(type) {
		case string:
			if i >= len(args) || args[i] != p {
				return false
			}
		case *string:
			if i >= len(args) {
				return false
			}
			*p = args[i]
		case *[]string:
			*p = args[i:]
			return true
		}
	}
	return len(args) == len(pattern)
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

	metadata := entryMetadata{
		Timestamp: time.Now().Unix(),
		MIME:      os.Getenv("CLIPBOARD_TYPE"),
		Image:     decodeImageMetadata(input),
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode metadata: %w", err)
	}
	mb := tx.Bucket([]byte(metadataBucketKey))
	b := tx.Bucket([]byte(bucketKey))

	if err := deduplicate(b, input, metadata.MIME, maxDedupeSearch); err != nil {
		return fmt.Errorf("deduplicating: %w", err)
	}
	id, err := b.NextSequence()
	if err != nil {
		return fmt.Errorf("getting next sequence: %w", err)
	}
	if err := b.Put(itob(id), input); err != nil {
		return fmt.Errorf("insert stdin: %w", err)
	}
	if err := mb.Put(itob(id), encoded); err != nil {
		return fmt.Errorf("insert metadata: %w", err)
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
		if err := deleteEntry(b.Tx(), k); err != nil {
			return err
		}
		seen++
	}
	return nil
}

func deduplicate(b *bolt.Bucket, input []byte, mimeType string, maxDedupeSearch uint64) error {
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
		metadata, err := compatReadMetadata(b.Tx(), k, v)
		if err != nil {
			return err
		}
		if metadata.MIME == mimeType {
			if err := deleteEntry(b.Tx(), k); err != nil {
				return err
			}
		}
		seen++
	}
	return nil
}

func list(dbPath string, out io.Writer, previewWidth uint, fields []string, input string) error {
	if len(fields) == 0 {
		return errors.New("please provide at least one field")
	}
	for _, field := range fields {
		switch field {
		case "id", "preview", "timestamp", "mime":
		default:
			return fmt.Errorf("unknown field %q", field)
		}
	}
	var id uint64
	if input != "" {
		var err error
		id, err = strconv.ParseUint(input, 10, 64)
		if err != nil {
			return fmt.Errorf("converting id: %w", err)
		}
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
	if input != "" && (b == nil || b.Get(itob(id)) == nil) {
		return fmt.Errorf("id %d not found", id)
	}
	if b == nil {
		return nil
	}
	c := b.Cursor()
	k, v := c.Last()
	if input != "" {
		k, v = c.Seek(itob(id))
	}
	for ; k != nil; k, v = c.Prev() {
		metadata, err := compatReadMetadata(tx, k, v)
		if err != nil {
			return err
		}
		values := make([]string, 0, len(fields))
		for _, field := range fields {
			var value string
			switch field {
			case "id":
				value = strconv.FormatUint(btoi(k), 10)
			case "preview":
				value = preview(v, metadata, previewWidth)
			case "timestamp":
				if metadata.Timestamp != 0 {
					value = strconv.FormatInt(metadata.Timestamp, 10)
				}
			case "mime":
				value = metadata.MIME
			}
			values = append(values, value)
		}
		if _, err := fmt.Fprintln(out, strings.Join(values, fieldSep)); err != nil {
			return fmt.Errorf("writing out: %w", err)
		}
		if input != "" {
			break
		}
	}
	return nil
}

const fieldSep = "\t"

func extractID(input string) (uint64, error) {
	idStr, _, _ := strings.Cut(strings.TrimSpace(input), fieldSep)
	if idStr == "" {
		return 0, fmt.Errorf("input not prefixed with id")
	}
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("converting id: %w", err)
	}
	return id, nil
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
	for k, v := c.Last(); k != nil; k, v = c.Prev() {
		if bytes.Contains(v, []byte(query)) {
			if err := deleteEntry(tx, k); err != nil {
				return err
			}
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
	if k != nil {
		if err := deleteEntry(tx, k); err != nil {
			return err
		}
	}

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

	sc := bufio.NewScanner(bytes.NewReader(input))
	for sc.Scan() {
		id, err := extractID(sc.Text())
		if err != nil {
			return fmt.Errorf("extract id: %w", err)
		}
		if err := deleteEntry(tx, itob(id)); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read ids: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func deleteEntry(tx *bolt.Tx, id []byte) error {
	if err := tx.Bucket([]byte(bucketKey)).Delete(id); err != nil {
		return fmt.Errorf("delete payload: %w", err)
	}
	if err := tx.Bucket([]byte(metadataBucketKey)).Delete(id); err != nil {
		return fmt.Errorf("delete metadata: %w", err)
	}
	return nil
}

func wipeAndCompact(dbPath string, olderThan time.Duration) error {
	if err := wipe(dbPath, olderThan); err != nil {
		return fmt.Errorf("wipe: %w", err)
	}
	if err := compactDB(dbPath); err != nil {
		return fmt.Errorf("compact: %w", err)
	}
	return nil
}

func wipe(dbPath string, olderThan time.Duration) error {
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

	cutoff := time.Now().Add(-olderThan).Unix()

	mb := tx.Bucket([]byte(metadataBucketKey))
	b := tx.Bucket([]byte(bucketKey))
	c := b.Cursor()
	for k, _ := c.Last(); k != nil; k, _ = c.Prev() {
		// entries without a timestamp predate metadata, so they're older than any cutoff
		if olderThan > 0 && mb != nil && mb.Get(k) != nil {
			metadata, err := readMetadata(tx, k)
			if err != nil {
				return err
			}
			if metadata.Timestamp >= cutoff {
				continue
			}
		}
		if err := deleteEntry(tx, k); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

const (
	bucketKey         = "b"
	metadataBucketKey = "metadata"
)

type entryMetadata struct {
	Timestamp int64              `json:"timestamp"`
	MIME      string             `json:"mime"`
	Image     entryImageMetadata `json:"image"`
}

type entryImageMetadata struct {
	Format string `json:"format,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

func readMetadata(tx *bolt.Tx, id []byte) (*entryMetadata, error) {
	data := tx.Bucket([]byte(metadataBucketKey)).Get(id)
	var metadata entryMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("read metadata for id %d: %w", btoi(id), err)
	}
	return &metadata, nil
}

// TODO: delete once pre metadata databases no longer need support
func compatReadMetadata(tx *bolt.Tx, id, payload []byte) (*entryMetadata, error) {
	b := tx.Bucket([]byte(metadataBucketKey))
	if b == nil || b.Get(id) == nil {
		return &entryMetadata{Image: decodeImageMetadata(payload)}, nil
	}
	return readMetadata(tx, id)
}

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

	db, err := bolt.Open(path, 0600, &bolt.Options{
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
		for _, bucket := range []string{bucketKey, metadataBucketKey} {
			if _, err := tx.CreateBucketIfNotExists([]byte(bucket)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("init bucket: %w", err)
	}
	return db, nil
}

func compactDB(path string) error {
	srcDB, err := bolt.Open(path, 0600, &bolt.Options{
		ReadOnly: true,
		Timeout:  1 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("open source db: %w", err)
	}
	defer srcDB.Close()

	tmpPath := path + ".tmp"
	dstDB, err := bolt.Open(tmpPath, 0600, &bolt.Options{
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

func preview(data []byte, metadata *entryMetadata, width uint) string {
	img := metadata.Image
	if img.Format != "" {
		return fmt.Sprintf("[[ binary data %s %s %dx%d ]]",
			sizeStr(len(data)), img.Format, img.Width, img.Height)
	}
	prev := string(data)
	prev = strings.TrimSpace(prev)
	prev = strings.Join(strings.Fields(prev), " ")
	return trunc(prev, int(width), "…")
}

func decodeImageMetadata(data []byte) entryImageMetadata {
	var img entryImageMetadata
	if config, format, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		img.Format = format
		img.Width = config.Width
		img.Height = config.Height
	}
	return img
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

type sizeParser struct{ *uint64 }

func (s sizeParser) Set(value string) error {
	value = strings.TrimSpace(value)
	for _, unit := range sizeUnits {
		before, found := strings.CutSuffix(strings.ToLower(value), strings.ToLower(unit.suffix))
		if !found {
			continue
		}
		num, err := strconv.ParseFloat(strings.TrimSpace(before), 64)
		if err != nil || num < 0 {
			return errors.New("number must be positive")
		}
		*s.uint64 = uint64(num * float64(unit.multiplier))
		return nil
	}
	num, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return errors.New("must be a number of bytes, or suffixed with a size unit (B, KB, KiB, MB, MiB, GB, GiB)")
	}
	*s.uint64 = num
	return nil
}

func (s sizeParser) String() string {
	if s.uint64 == nil {
		return ""
	}
	for _, unit := range sizeUnits {
		if *s.uint64 >= unit.multiplier && *s.uint64%unit.multiplier == 0 {
			return strconv.FormatUint(*s.uint64/unit.multiplier, 10) + unit.suffix
		}
	}
	return strconv.FormatUint(*s.uint64, 10)
}

// ordered from longest to shortest suffix to avoid partial matches
var sizeUnits = []struct {
	suffix     string
	multiplier uint64
}{
	{"GiB", 1024 * 1024 * 1024},
	{"MiB", 1024 * 1024},
	{"KiB", 1024},
	{"GB", 1000 * 1000 * 1000},
	{"MB", 1000 * 1000},
	{"KB", 1000},
	{"B", 1},
}
