package vm

import (
	"fmt"

	"github.com/tendermint/tendermint/rpc/client"
	coretypes "github.com/tendermint/tendermint/rpc/core/types"
	"github.com/tendermint/tendermint/store"
)

var (
	// see README
	defaultPerPage = 30
	maxPerPage     = 100
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

func validatePage(pagePtr *int, perPage, totalCount int) (int, error) {
	if perPage < 1 {
		panic(fmt.Sprintf("zero or negative perPage: %d", perPage))
	}

	if pagePtr == nil { // no page parameter
		return 1, nil
	}

	pages := ((totalCount - 1) / perPage) + 1
	if pages == 0 {
		pages = 1 // one page (even if it's empty)
	}
	page := *pagePtr
	if page <= 0 || page > pages {
		return 1, fmt.Errorf("page should be within [1, %d] range, given %d", pages, page)
	}

	return page, nil
}

func validatePerPage(perPagePtr *int) int {
	if perPagePtr == nil { // no per_page parameter
		return defaultPerPage
	}

	perPage := *perPagePtr
	if perPage < 1 {
		return defaultPerPage
	} else if perPage > maxPerPage {
		return maxPerPage
	}
	return perPage
}

func validateSkipCount(page, perPage int) int {
	skipCount := (page - 1) * perPage
	if skipCount < 0 {
		return 0
	}
	return skipCount
}

func WaitForHeight(c Service, h int64, waiter client.Waiter) error {
	if waiter == nil {
		waiter = client.DefaultWaitStrategy
	}
	delta := int64(1)
	for delta > 0 {
		r := new(coretypes.ResultStatus)
		if err := c.Status(nil, nil, r); err != nil {
			return err
		}
		delta = h - r.SyncInfo.LatestBlockHeight
		// wait for the time, or abort early
		if err := waiter(delta); err != nil {
			return err
		}
	}
	return nil
}
