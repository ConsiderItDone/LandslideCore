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
	"github.com/ava-labs/avalanchego/vms/components/chain"
	"github.com/stretchr/testify/assert"
	"github.com/tendermint/tendermint/abci/example/counter"
	"github.com/tendermint/tendermint/abci/types"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
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

	blk0, err := vm.BuildBlock(context.Background())
	assert.ErrorIs(t, err, errNoPendingTxs, "expecting error no txs")
	assert.Nil(t, blk0)

	service := Service{vm}

	// submit first tx (0x00)
	args := &BroadcastTxArgs{
		Tx: []byte{0x00},
	}
	reply := &ctypes.ResultBroadcastTx{}
	err = service.BroadcastTxSync(nil, args, reply)
	assert.NoError(t, err)
	assert.Equal(t, types.CodeTypeOK, reply.Code)

	// build 1st block
	blk1, err := vm.BuildBlock(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, blk1)

	err = blk1.Accept(context.Background())
	assert.NoError(t, err)

	tmBlk1 := blk1.(*chain.BlockWrapper).Block.(*Block).tmBlock

	t.Logf("Block: %d", blk1.Height())
	t.Logf("TM Block Tx count: %d", len(tmBlk1.Data.Txs))

	// submit second tx (0x01)
	args = &BroadcastTxArgs{
		Tx: []byte{0x01},
	}
	reply = &ctypes.ResultBroadcastTx{}
	err = service.BroadcastTxSync(nil, args, reply)
	assert.NoError(t, err)
	assert.Equal(t, types.CodeTypeOK, reply.Code)

	// submit 3rd tx (0x02)
	args = &BroadcastTxArgs{
		Tx: []byte{0x02},
	}
	reply = &ctypes.ResultBroadcastTx{}
	err = service.BroadcastTxSync(nil, args, reply)
	assert.NoError(t, err)
	assert.Equal(t, types.CodeTypeOK, reply.Code)

	// build second block with 2 TX together
	blk2, err := vm.BuildBlock(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, blk2)

	err = blk2.Accept(context.Background())
	assert.NoError(t, err)

	tmBlk2 := blk2.(*chain.BlockWrapper).Block.(*Block).tmBlock

	t.Logf("Block: %d", blk2.Height())
	t.Logf("TM Block Tx count: %d", len(tmBlk2.Data.Txs))
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
