package vm

import (
	"context"
	"fmt"
	"github.com/ava-labs/avalanchego/database/manager"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/version"
	"github.com/stretchr/testify/assert"
	"github.com/tendermint/tendermint/abci/example/counter"
	"os"
	"testing"
)

var (
	blockchainID = ids.ID{1, 2, 3}
	genesis      = "{\"genesis_time\":\"2023-03-04T03:46:06.533236098Z\",\"chain_id\":\"test-chain-U8te75\",\"initial_height\":\"0\",\"consensus_params\":{\"block\":{\"max_bytes\":\"22020096\",\"max_gas\":\"-1\",\"time_iota_ms\":\"1000\"},\"evidence\":{\"max_age_num_blocks\":\"100000\",\"max_age_duration\":\"172800000000000\",\"max_bytes\":\"1048576\"},\"validator\":{\"pub_key_types\":[\"ed25519\"]},\"version\":{}},\"app_hash\":\"\"}"
)

func TestInitVm(t *testing.T) {
	//ctx := context.TODO()
	// Initialize the vm
	vm, _, _, err := newTestVM()
	assert.NoError(t, err)
	assert.NotNil(t, vm)

	blk, err := vm.BuildBlock(context.Background())
	assert.ErrorIs(t, err, errNoPendingTxs, "expecting error no txs")
	assert.Nil(t, blk)
	//t.Logf("Build block %v, err: %v", blk, err)
	//vm.SetState()

	//lastAcceptedID, err := vm.LastAccepted(context.Background())
	//assert.NoError(t, err)
	//
	//t.Log(lastAcceptedID)
}

func newTestVM() (*VM, *snow.Context, chan common.Message, error) {
	dbManager := manager.NewMemDB(&version.Semantic{
		Major: 1,
		Minor: 0,
		Patch: 0,
	})
	msgChan := make(chan common.Message, 1)
	vm := NewVM(counter.NewApplication(true))
	snowCtx := snow.DefaultContextTest()
	snowCtx.Log = logging.NewLogger(
		fmt.Sprintf("<%s Chain>", blockchainID),
		logging.NewWrappedCore(
			logging.Info,
			os.Stderr,
			logging.Colors.ConsoleEncoder(),
		),
	)
	snowCtx.ChainID = blockchainID
	err := vm.Initialize(context.TODO(), snowCtx, dbManager, []byte(genesis), nil, nil, msgChan, nil, nil)

	return vm, snowCtx, msgChan, err
}
