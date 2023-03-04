package vm

import (
	"github.com/ava-labs/avalanchego/database"
	dbm "github.com/tendermint/tm-db"
)

var _ dbm.DB = &Database{}

type Database struct {
	database.Database
}

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
	//TODO implement me
	panic("implement me")
}

func (db Database) ReverseIterator(start, end []byte) (dbm.Iterator, error) {
	//TODO implement me
	panic("implement me")
}

func (db Database) NewBatch() dbm.Batch {
	//TODO implement me
	panic("implement me")
}

func (db Database) Print() error {
	//TODO implement me
	panic("implement me")
}

func (db Database) Stats() map[string]string {
	//TODO implement me
	panic("implement me")
}
