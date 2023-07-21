package database

import (
	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/database/leveldb"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/prometheus/client_golang/prometheus"
)

// These are valid backend types.
const (
	// GoLevelDBBackend represents goleveldb (github.com/syndtr/goleveldb - most
	// popular implementation)
	//   - pure go
	//   - stable
	GoLevelDBBackend string = "goleveldb"
	// CLevelDBBackend represents cleveldb (uses levigo wrapper)
	//   - fast
	//   - requires gcc
	//   - use cleveldb build tag (go build -tags cleveldb)
	CLevelDBBackend string = "cleveldb"
	// MemDBBackend represents in-memory key value store, which is mostly used
	// for testing.
	MemDBBackend string = "memdb"
	// BoltDBBackend represents bolt (uses etcd's fork of bolt -
	// github.com/etcd-io/bbolt)
	//   - EXPERIMENTAL
	//   - may be faster is some use-cases (random reads - indexer)
	//   - use boltdb build tag (go build -tags boltdb)
	BoltDBBackend string = "boltdb"
	// RocksDBBackend represents rocksdb (uses github.com/tecbot/gorocksdb)
	//   - EXPERIMENTAL
	//   - requires gcc
	//   - use rocksdb build tag (go build -tags rocksdb)
	RocksDBBackend string = "rocksdb"

	BadgerDBBackend string = "badgerdb"
)

// NewDB creates a new database of type backend with the given name.
func NewDB(name string, backendType, dir string) (database.Database, error) {
	var err error
	var db database.Database
	switch backendType {
	case GoLevelDBBackend:
		logger := logging.NewLogger(name)
		db, err = leveldb.New(dir, []byte{}, logger, name, prometheus.NewRegistry())
		if err != nil {
			return nil, err
		}
	case MemDBBackend:
	case BadgerDBBackend:
	default:

	}
	return db, nil
}
