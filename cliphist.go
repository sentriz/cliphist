package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
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

	"github.com/rivo/uniseg"
	bolt "go.etcd.io/bbolt"
	"go.senan.xyz/flagconf"
	"golang.org/x/image/draw"
)

//go:embed version.txt
var version string

//nolint:errcheck
func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  $ %s <store|list|decode|delete|delete-query|wipe|rebuild-thumbnails|version>\n", flag.CommandLine.Name())
		fmt.Fprintf(flag.CommandLine.Output(), "options:\n")
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
	dbPath := flag.String("db-path", filepath.Join(cacheHome, "cliphist", "db"), "path to db")
	configPath := flag.String("config-path", filepath.Join(configHome, "cliphist", "config"), "overwrite config path to use instead of cli flags")
	thumbnailPath := flag.String("thumbnail-path", filepath.Join(cacheHome, "cliphist", "thumbnails"), "path to thumbnail cache directory")
	thumbnailSize := flag.Uint("thumbnail-size", 64, "thumbnail size in pixels (square)")

	flag.Parse()
	flagconf.ParseEnv()
	flagconf.ParseConfig(*configPath)

	switch flag.Arg(0) {
	case "store":
		switch os.Getenv("CLIPBOARD_STATE") { // from man wl-clipboard
		case "sensitive":
		case "clear":
			err = deleteLast(*dbPath, *thumbnailPath)
		default:
			err = store(*dbPath, *thumbnailPath, os.Stdin, *maxDedupeSearch, *maxItems, *minLength, *thumbnailSize)
		}
	case "list":
		err = list(*dbPath, *thumbnailPath, os.Stdout, *previewWidth)
	case "decode":
		err = decode(*dbPath, os.Stdin, os.Stdout, flag.Arg(1))
	case "delete-query":
		err = deleteQuery(*dbPath, *thumbnailPath, flag.Arg(1))
	case "delete":
		err = delete(*dbPath, *thumbnailPath, os.Stdin)
	case "wipe":
		err = wipeAndCompact(*dbPath, *thumbnailPath)
	case "rebuild-thumbnails":
		err = rebuildThumbnails(*dbPath, *thumbnailPath, *thumbnailSize)
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

func store(dbPath, thumbPath string, in io.Reader, maxDedupeSearch, maxItems uint64, minLength, thumbSize uint) error {
	input, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	if len(input) > 5*1e6 { // don't store >5MB
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

	deletedIDs, err := deduplicateWithIDs(b, input, maxDedupeSearch)
	if err != nil {
		return fmt.Errorf("deduplicating: %w", err)
	}
	// Clean up thumbnails for deduplicated entries
	for _, delID := range deletedIDs {
		deleteThumbnail(thumbPath, delID)
	}

	id, err := b.NextSequence()
	if err != nil {
		return fmt.Errorf("getting next sequence: %w", err)
	}
	if err := b.Put(itob(id), input); err != nil {
		return fmt.Errorf("insert stdin: %w", err)
	}

	trimmedIDs, err := trimLengthWithIDs(b, maxItems)
	if err != nil {
		return fmt.Errorf("trimming length: %w", err)
	}
	// Clean up thumbnails for trimmed entries
	for _, trimID := range trimmedIDs {
		deleteThumbnail(thumbPath, trimID)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	// Generate thumbnail if this is an image
	if _, _, err := image.DecodeConfig(bytes.NewReader(input)); err == nil {
		if err := generateThumbnail(thumbPath, id, input, thumbSize); err != nil {
			// Non-fatal: log but don't fail the store
			fmt.Fprintf(os.Stderr, "warning: failed to generate thumbnail: %v\n", err)
		}
	}

	return nil
}

// trim the store's size to a number of max items. manually counting
// seen items because we can't rely on sequence numbers when items can
// be deleted when deduplicating
func trimLength(b *bolt.Bucket, maxItems uint64) error {
	_, err := trimLengthWithIDs(b, maxItems)
	return err
}

func trimLengthWithIDs(b *bolt.Bucket, maxItems uint64) ([]uint64, error) {
	var deletedIDs []uint64
	c := b.Cursor()
	var seen uint64
	for k, _ := c.Last(); k != nil; k, _ = c.Prev() {
		if seen < maxItems {
			seen++
			continue
		}
		deletedIDs = append(deletedIDs, btoi(k))
		if err := b.Delete(k); err != nil {
			return deletedIDs, fmt.Errorf("delete :%w", err)
		}
		seen++
	}
	return deletedIDs, nil
}

func deduplicate(b *bolt.Bucket, input []byte, maxDedupeSearch uint64) error {
	_, err := deduplicateWithIDs(b, input, maxDedupeSearch)
	return err
}

func deduplicateWithIDs(b *bolt.Bucket, input []byte, maxDedupeSearch uint64) ([]uint64, error) {
	var deletedIDs []uint64
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
		deletedIDs = append(deletedIDs, btoi(k))
		if err := b.Delete(k); err != nil {
			return deletedIDs, fmt.Errorf("delete :%w", err)
		}
		seen++
	}
	return deletedIDs, nil
}

// generateThumbnail creates a thumbnail for an image and saves it to the thumbnail directory
func generateThumbnail(thumbPath string, id uint64, data []byte, size uint) error {
	if err := os.MkdirAll(thumbPath, 0700); err != nil {
		return fmt.Errorf("create thumbnail dir: %w", err)
	}

	// Decode the original image
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("decode image: %w", err)
	}

	// Calculate thumbnail dimensions maintaining aspect ratio
	bounds := img.Bounds()
	origW, origH := bounds.Dx(), bounds.Dy()
	thumbW, thumbH := int(size), int(size)

	if origW > origH {
		thumbH = int(float64(origH) * float64(size) / float64(origW))
	} else {
		thumbW = int(float64(origW) * float64(size) / float64(origH))
	}

	// Create thumbnail
	thumb := image.NewRGBA(image.Rect(0, 0, thumbW, thumbH))
	draw.CatmullRom.Scale(thumb, thumb.Bounds(), img, bounds, draw.Over, nil)

	// Save thumbnail
	thumbFile := filepath.Join(thumbPath, fmt.Sprintf("%d.png", id))
	f, err := os.Create(thumbFile)
	if err != nil {
		return fmt.Errorf("create thumbnail file: %w", err)
	}
	defer f.Close()

	if err := png.Encode(f, thumb); err != nil {
		return fmt.Errorf("encode thumbnail: %w", err)
	}

	return nil
}

// deleteThumbnail removes a thumbnail file if it exists
func deleteThumbnail(thumbPath string, id uint64) {
	thumbFile := filepath.Join(thumbPath, fmt.Sprintf("%d.png", id))
	os.Remove(thumbFile) // ignore errors, file might not exist
}

// thumbnailPath returns the path to a thumbnail if it exists, empty string otherwise
func getThumbnailPath(thumbPath string, id uint64) string {
	thumbFile := filepath.Join(thumbPath, fmt.Sprintf("%d.png", id))
	if _, err := os.Stat(thumbFile); err == nil {
		return thumbFile
	}
	return ""
}

// rebuildThumbnails generates thumbnails for all existing image entries
func rebuildThumbnails(dbPath, thumbPath string, thumbSize uint) error {
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

	var count int
	for k, v := c.First(); k != nil; k, v = c.Next() {
		id := btoi(k)
		// Check if this is an image
		if _, _, err := image.DecodeConfig(bytes.NewReader(v)); err == nil {
			// Check if thumbnail already exists
			if getThumbnailPath(thumbPath, id) == "" {
				if err := generateThumbnail(thumbPath, id, v, thumbSize); err != nil {
					fmt.Fprintf(os.Stderr, "warning: failed to generate thumbnail for %d: %v\n", id, err)
				} else {
					count++
				}
			}
		}
	}

	fmt.Fprintf(os.Stdout, "generated %d thumbnails\n", count)
	return nil
}

func list(dbPath, thumbPath string, out io.Writer, previewWidth uint) error {
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
		id := btoi(k)
		previewStr := preview(id, v, previewWidth)
		// For images, append thumbnail path using rofi's icon format
		if thumbFile := getThumbnailPath(thumbPath, id); thumbFile != "" {
			fmt.Fprintf(out, "%s\x00icon\x1f%s\n", previewStr, thumbFile)
		} else {
			fmt.Fprintln(out, previewStr)
		}
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

func deleteQuery(dbPath, thumbPath string, query string) error {
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

	var deletedIDs []uint64
	b := tx.Bucket([]byte(bucketKey))
	c := b.Cursor()
	for k, v := c.First(); k != nil; k, v = c.Next() {
		if bytes.Contains(v, []byte(query)) {
			deletedIDs = append(deletedIDs, btoi(k))
			_ = b.Delete(k)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	// Clean up thumbnails
	for _, id := range deletedIDs {
		deleteThumbnail(thumbPath, id)
	}
	return nil
}

func deleteLast(dbPath, thumbPath string) error {
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
	var deletedID uint64
	if k != nil {
		deletedID = btoi(k)
	}
	_ = b.Delete(k)

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	// Clean up thumbnail
	if deletedID > 0 {
		deleteThumbnail(thumbPath, deletedID)
	}
	return nil
}

func delete(dbPath, thumbPath string, in io.Reader) error {
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

	var deletedIDs []uint64
	for sc := bufio.NewScanner(bytes.NewReader(input)); sc.Scan(); {
		id, err := extractID(sc.Text())
		if err != nil {
			return fmt.Errorf("extract id: %w", err)
		}
		deletedIDs = append(deletedIDs, id)
		b := tx.Bucket([]byte(bucketKey))
		if err := b.Delete(itob(id)); err != nil {
			return fmt.Errorf("delete key: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	// Clean up thumbnails
	for _, id := range deletedIDs {
		deleteThumbnail(thumbPath, id)
	}
	return nil
}

func wipeAndCompact(dbPath, thumbPath string) error {
	if err := wipe(dbPath, thumbPath); err != nil {
		return fmt.Errorf("wipe: %w", err)
	}
	if err := compactDB(dbPath); err != nil {
		return fmt.Errorf("compact: %w", err)
	}
	return nil
}

func wipe(dbPath, thumbPath string) error {
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

	// Wipe thumbnails directory
	if thumbPath != "" {
		os.RemoveAll(thumbPath)
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
