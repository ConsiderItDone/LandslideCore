package vm

import (
	"fmt"

	"github.com/tendermint/tendermint/store"
)

// bsHeight can be either latest committed or uncommitted (+1) height.
func getHeight(bs *store.BlockStore, heightPtr *int64) (int64, error) {
	bsHeight := bs.Height()
	bsBase := bs.Base()
	if heightPtr != nil {
		height := *heightPtr
		if height <= 0 {
			return 0, fmt.Errorf("height must be greater than 0, but got %d", height)
		}
		if height > bsHeight {
			return 0, fmt.Errorf("height %d must be less than or equal to the current blockchain height %d", height, bsHeight)
		}
		if height < bsBase {
			return 0, fmt.Errorf("height %d is not available, lowest height is %d", height, bsBase)
		}
		return height, nil
	}
	return bsHeight, nil
}
