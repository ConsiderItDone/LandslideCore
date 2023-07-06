package vm

import (
	"github.com/ava-labs/avalanchego/database"
	dbm "github.com/tendermint/tm-db"
)

var (
	_ dbm.DB = &Database{}
)

type (
	Database struct {
		database.Database
	}
	Iterator struct {
		database.Iterator

		start []byte
		end   []byte
	}
	Batch struct {
		database.Batch
	}
)

func (db Database) Get(key []byte) ([]byte, error) {
	res, err := db.Database.Get(key)
	if err != nil {
		if err.Error() == "not found" {
			return nil, nil
		}
		return nil, err
	}
	return res, nil
}

func (db Database) Set(key []byte, value []byte) error {
	return db.Database.Put(key, value)
}

func (db Database) SetSync(key []byte, value []byte) error {
	return db.Database.Put(key, value)
}

func (db Database) DeleteSync(key []byte) error {
	return db.Database.Delete(key)
}

func (db Database) Iterator(start, end []byte) (dbm.Iterator, error) {
	return Iterator{db.Database.NewIteratorWithStart(start), start, end}, nil
}

func (db Database) ReverseIterator(start, end []byte) (dbm.Iterator, error) {
	return Iterator{db.Database.NewIteratorWithStart(start), start, end}, nil
}

func (db Database) NewBatch() dbm.Batch {
	return Batch{db.Database.NewBatch()}
}

func (db Database) Print() error {
	//TODO implement me
	return nil
}

func (db Database) Stats() map[string]string {
	//TODO implement me
	return nil
}

func (iter Iterator) Domain() (start []byte, end []byte) {
	return iter.start, iter.end
}

func (iter Iterator) Valid() bool {
	return iter.Iterator.Error() == nil && len(iter.Iterator.Key()) > 0
}

func (iter Iterator) Next() {
	iter.Iterator.Next()
}

func (iter Iterator) Key() (key []byte) {
	return iter.Iterator.Key()
}

func (iter Iterator) Value() (value []byte) {
	return iter.Iterator.Value()
}

func (iter Iterator) Error() error {
	return iter.Iterator.Error()
}

func (iter Iterator) Close() error {
	iter.Iterator.Release()
	return iter.Error()
}

func (b Batch) Set(key, value []byte) error {
	return b.Batch.Put(key, value)
}

func (b Batch) Delete(key []byte) error {
	return b.Batch.Delete(key)
}

func (b Batch) Write() error {
	return b.Batch.Write()
}

func (b Batch) WriteSync() error {
	return b.Batch.Write()
}

func (b Batch) Close() error {
	return nil
}
