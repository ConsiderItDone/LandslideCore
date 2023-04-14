package psql

import (
	"github.com/consideritdone/landslidecore/state/indexer"
	"github.com/consideritdone/landslidecore/state/txindex"
)

var (
	_ indexer.BlockIndexer = BackportBlockIndexer{}
	_ txindex.TxIndexer    = BackportTxIndexer{}
)
