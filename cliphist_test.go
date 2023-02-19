package main

import (
	"bytes"
	"image"
	"image/png"
	"path/filepath"
	"testing"

	"github.com/matryer/is"
	bolt "go.etcd.io/bbolt"
)

func TestList(t *testing.T) {
	t.Parallel()
	db := initDBt(t)
	defer db.Close()

	is := is.New(t)

	is.NoErr(store(db, []byte("one"), 100, 100))
	is.NoErr(store(db, []byte("two"), 100, 100))
	is.NoErr(store(db, []byte("three"), 100, 100))

	var buff bytes.Buffer
	is.NoErr(list(db, &buff))

	items := splitn(buff.Bytes())
	is.Equal(len(items), 3)                                 // we have 3 items
	is.Equal(string(items[0]), preview(3, []byte("three"))) // last in first out
}

func TestStoreLimit(t *testing.T) {
	t.Parallel()
	db := initDBt(t)
	defer db.Close()

	is := is.New(t)

	const maxItems = 10
	const maxDedupeSearch = 100
	const toStore = 20

	for i := uint64(0); i < toStore; i++ {
		is.NoErr(store(db, itob(i+1), maxDedupeSearch, maxItems))
	}

	var buff bytes.Buffer
	is.NoErr(list(db, &buff))

	items := splitn(buff.Bytes())
	is.Equal(len(items), maxItems)                                               // we threw away all but the max
	is.Equal(string(items[0]), preview(toStore, itob(toStore)))                  // last in first out
	is.Equal(string(items[len(items)-1]), preview(maxItems+1, itob(maxItems+1))) // last is middle
}

func TestDeduplicate(t *testing.T) {
	t.Parallel()
	db := initDBt(t)
	defer db.Close()

	is := is.New(t)

	const maxItems = 200
	const maxDedupeSearch = 200

	is.NoErr(store(db, []byte("hello"), maxDedupeSearch, maxItems))
	is.NoErr(store(db, []byte("multiple"), maxDedupeSearch, maxItems))
	is.NoErr(store(db, []byte("multiple"), maxDedupeSearch, maxItems))
	is.NoErr(store(db, []byte("multiple"), maxDedupeSearch, maxItems))
	is.NoErr(store(db, []byte("hello"), maxDedupeSearch, maxItems))

	var buff bytes.Buffer
	is.NoErr(list(db, &buff))

	items := splitn(buff.Bytes())
	is.Equal(len(items), 2)                                                    // we have 2 unique
	is.Equal(extractItemt(t, db, extractIDt(t, items[0])), []byte("hello"))    // first came forward
	is.Equal(extractItemt(t, db, extractIDt(t, items[1])), []byte("multiple")) // middle stayed
}

func TestDeleteQuery(t *testing.T) {
	t.Parallel()
	db := initDBt(t)
	defer db.Close()

	is := is.New(t)

	const maxItems = 200
	const maxDedupeSearch = 200

	is.NoErr(store(db, []byte("aa hello 1"), maxDedupeSearch, maxItems))
	is.NoErr(store(db, []byte("bb hello 2"), maxDedupeSearch, maxItems))
	is.NoErr(store(db, []byte("aa hello 3"), maxDedupeSearch, maxItems))
	is.NoErr(store(db, []byte("bb hello 4"), maxDedupeSearch, maxItems))
	is.NoErr(store(db, []byte("aa hello 5"), maxDedupeSearch, maxItems))

	is.NoErr(deleteQuery(db, "bb"))

	var buff bytes.Buffer
	is.NoErr(list(db, &buff))

	items := splitn(buff.Bytes())
	is.Equal(len(items), 3) // we deleted the bbs
	for _, item := range items {
		is.True(bytes.Contains(item, []byte("aa"))) // item is aa
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()
	db := initDBt(t)
	defer db.Close()

	is := is.New(t)

	const maxItems = 200
	const maxDedupeSearch = 200

	is.NoErr(store(db, []byte("aa hello 1"), maxDedupeSearch, maxItems))
	is.NoErr(store(db, []byte("bb hello 2"), maxDedupeSearch, maxItems))
	is.NoErr(store(db, []byte("aa hello 3"), maxDedupeSearch, maxItems))
	is.NoErr(store(db, []byte("bb hello 4"), maxDedupeSearch, maxItems))
	is.NoErr(store(db, []byte("aa hello 5"), maxDedupeSearch, maxItems))

	is.NoErr(delete(db, []byte("3\taa hello 3")))

	var buff bytes.Buffer
	is.NoErr(list(db, &buff))

	items := splitn(buff.Bytes())
	is.Equal(len(items), 4) // we deleted no. 3
	for _, item := range items {
		id, err := extractID(item)
		is.NoErr(err)    // we don't have no. 3 anymore
		is.True(id != 3) // we don't have no. 3 anymore
	}
}

func TestWipe(t *testing.T) {
	t.Parallel()
	db := initDBt(t)
	defer db.Close()

	is := is.New(t)

	const maxItems = 200
	const maxDedupeSearch = 200

	is.NoErr(store(db, []byte("aa hello 1"), maxDedupeSearch, maxItems))
	is.NoErr(store(db, []byte("bb hello 2"), maxDedupeSearch, maxItems))
	is.NoErr(store(db, []byte("aa hello 3"), maxDedupeSearch, maxItems))
	is.NoErr(store(db, []byte("bb hello 4"), maxDedupeSearch, maxItems))
	is.NoErr(store(db, []byte("aa hello 5"), maxDedupeSearch, maxItems))

	is.NoErr(wipe(db))

	var buff bytes.Buffer
	is.NoErr(list(db, &buff))

	items := splitn(buff.Bytes())
	is.Equal(len(items), 0)
}

func TestBinary(t *testing.T) {
	t.Parallel()
	db := initDBt(t)
	defer db.Close()

	is := is.New(t)

	const maxItems = 200
	const maxDedupeSearch = 200

	var inBuff bytes.Buffer
	is.NoErr(png.Encode(&inBuff, image.NewRGBA(image.Rectangle{
		Min: image.Point{0, 0},
		Max: image.Point{20, 20},
	})))
	is.NoErr(store(db, inBuff.Bytes(), maxDedupeSearch, maxItems))

	var outBuff bytes.Buffer
	is.NoErr(decode(db, []byte(preview(1, []byte(nil))), &outBuff))

	is.Equal(inBuff, outBuff) // we didn't loose any bytes

	var listBuff bytes.Buffer
	is.NoErr(list(db, &listBuff))

	items := splitn(listBuff.Bytes())
	is.Equal(len(items), 1)                                // we have one image
	is.Equal(string(items[0]), "1\tbinary data image/png") // it looks like a png
}

func initDBt(t *testing.T) *bolt.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "db")
	db, err := bolt.Open(path, 0644, &bolt.Options{})
	if err != nil {
		t.Fatalf("opening db: %v", err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucket([]byte(bucketKey))
		return err
	})
	if err != nil {
		t.Fatalf("init bucket: %v", err)
	}
	return db
}

func extractIDt(t *testing.T, input []byte) uint64 {
	t.Helper()

	id, err := extractID(input)
	if err != nil {
		t.Fatalf("error extracting id from %q: %v", string(input), err)
	}
	return id
}

func extractItemt(t *testing.T, db *bolt.DB, id uint64) []byte {
	t.Helper()

	tx, err := db.Begin(false)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback() //nolint:errcheck

	b := tx.Bucket([]byte(bucketKey))
	return b.Get(itob(uint64(id)))
}

func splitn(b []byte) [][]byte {
	return bytes.FieldsFunc(b, func(r rune) bool {
		return r == '\n'
	})
}
