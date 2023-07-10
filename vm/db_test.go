package vm

import (
	"bytes"
	"fmt"
	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/database/prefixdb"
	dbm "github.com/tendermint/tm-db"
	"math/rand"
	"sync"
	"testing"
)

var (
	testDBPrefix = []byte("test")
)

func TestMemDB(t *testing.T) {
	vm, _, _ := mustNewKVTestVm(t)
	baseDB := vm.dbManager.Current().Database
	db := Database{prefixdb.NewNested(testDBPrefix, baseDB)}
	t.Run("PrefixDB", func(t *testing.T) { Run(t, db) })
	t.Run("BaseDB(MemDB)", func(t *testing.T) { RunAvaDatabase(t, baseDB) })
}

// Run generates concurrent reads and writes to db so the race detector can
// verify concurrent operations are properly synchronized.
// The contents of db are garbage after Run returns.
func Run(t *testing.T, db dbm.DB) {
	t.Helper()

	const numWorkers = 10
	const numKeys = 64

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()

			// Insert a bunch of keys with random data.
			for k := 1; k <= numKeys; k++ {
				key := taskKey(i, k) // say, "task-<i>-key-<k>"
				value := randomValue()
				if err := db.Set(key, value); err != nil {
					t.Errorf("Task %d: db.Set(%q=%q) failed: %v",
						i, string(key), string(value), err)
				}
			}

			// Iterate over the database to make sure our keys are there.
			it, err := db.Iterator(nil, nil)
			if err != nil {
				t.Errorf("Iterator[%d]: %v", i, err)
				return
			}
			found := make(map[string][]byte)
			mine := []byte(fmt.Sprintf("task-%d-", i))
			for {
				if key := it.Key(); bytes.HasPrefix(key, mine) {
					found[string(key)] = it.Value()
				}
				it.Next()
				if !it.Valid() {
					break
				}
			}
			if err := it.Error(); err != nil {
				t.Errorf("Iterator[%d] reported error: %v", i, err)
			}
			if err := it.Close(); err != nil {
				t.Errorf("Close iterator[%d]: %v", i, err)
			}
			if len(found) != numKeys {
				t.Errorf("Task %d: found %d keys, wanted %d", i, len(found), numKeys)
			}

			for key, value := range mine {
				fmt.Println("--")
				fmt.Println(key)
				fmt.Println(value)
				fmt.Println("--")
			}

			// Delete all the keys we inserted.
			for k := 1; k <= numKeys; k++ {
				key := taskKey(i, k) // say, "task-<i>-key-<k>"
				if err := db.Delete(key); err != nil {
					t.Errorf("Delete %q: %v", key, err)
				}
			}
			// Iterate over the database to make sure our keys are there.
			it, err = db.Iterator(nil, nil)
			if err != nil {
				t.Errorf("Iterator[%d]: %v", i, err)
				return
			}
			foundAfterRemoval := make(map[string][]byte)
			for {
				if key := it.Key(); bytes.HasPrefix(key, mine) {
					foundAfterRemoval[string(key)] = it.Value()
				}
				it.Next()
				if !it.Valid() {
					break
				}
			}
			if len(foundAfterRemoval) != 0 {
				t.Errorf("Values left after deletion: %v", foundAfterRemoval)
				return
			}
		}()
	}
	wg.Wait()
}

// Run generates concurrent reads and writes to db so the race detector can
// verify concurrent operations are properly synchronized.
// The contents of db are garbage after Run returns.
func RunAvaDatabase(t *testing.T, db database.Database) {
	t.Helper()

	const numWorkers = 10
	const numKeys = 64

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()

			// Insert a bunch of keys with random data.
			for k := 1; k <= numKeys; k++ {
				key := taskKey(i, k) // say, "task-<i>-key-<k>"
				value := randomValue()
				if err := db.Put(key, value); err != nil {
					t.Errorf("Task %d: db.Set(%q=%q) failed: %v",
						i, string(key), string(value), err)
				}
			}

			// Iterate over the database to make sure our keys are there.
			it := db.NewIterator()
			found := make(map[string][]byte)
			mine := []byte(fmt.Sprintf("task-%d-", i))
			for {
				if key := it.Key(); bytes.HasPrefix(key, mine) {
					found[string(key)] = it.Value()
				}
				it.Next()
				if !it.Next() {
					break
				}
			}
			if err := it.Error(); err != nil {
				t.Errorf("Iterator[%d] reported error: %v", i, err)
			}
			it.Release()

			if len(found) != numKeys {
				t.Errorf("Task %d: found %d keys, wanted %d", i, len(found), numKeys)
			}

			for key, value := range mine {
				fmt.Println("--")
				fmt.Println(key)
				fmt.Println(value)
				fmt.Println("--")
			}

			// Delete all the keys we inserted.
			for k := 1; k <= numKeys; k++ {
				key := taskKey(i, k) // say, "task-<i>-key-<k>"
				if err := db.Delete(key); err != nil {
					t.Errorf("Delete %q: %v", key, err)
				}
			}
			// Iterate over the database to make sure our keys are there.
			it = db.NewIterator()
			foundAfterRemoval := make(map[string][]byte)
			for {
				if key := it.Key(); bytes.HasPrefix(key, mine) {
					foundAfterRemoval[string(key)] = it.Value()
				}
				it.Next()
				if !it.Next() {
					break
				}
			}
			if len(foundAfterRemoval) != 0 {
				t.Errorf("Values left after deletion: %v", foundAfterRemoval)
				return
			}
		}()
	}
	wg.Wait()
}

func taskKey(i, k int) []byte {
	return []byte(fmt.Sprintf("task-%d-key-%d", i, k))
}

func randomValue() []byte {
	value := []byte("value-")
	dec := rand.Uint32()
	return []byte(fmt.Sprintf("%s%d", value, dec))
}
