package vm

import (
	"context"
	_ "embed"
	"fmt"
	"github.com/consideritdone/landslidecore/abci/example/kvstore"
	rpctypes "github.com/consideritdone/landslidecore/rpc/jsonrpc/types"
	"github.com/consideritdone/landslidecore/types"
	"os"
	"reflect"
	"testing"

	"github.com/ava-labs/avalanchego/database/manager"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/consideritdone/landslidecore/abci/example/counter"
	atypes "github.com/consideritdone/landslidecore/abci/types"
	tmrand "github.com/consideritdone/landslidecore/libs/rand"
)

var (
	blockchainID = ids.ID{1, 2, 3}

	//go:embed data/vm_test_genesis.json
	genesis string
)

func newCounterTestVM() (*VM, *snow.Context, chan common.Message, error) {
	app := counter.NewApplication(true)

	return newTestVM(app)
}

func newKVTestVM() (*VM, *snow.Context, chan common.Message, error) {
	app := kvstore.NewApplication()

	return newTestVM(app)
}

func newTestVM(app atypes.Application) (*VM, *snow.Context, chan common.Message, error) {
	dbManager := manager.NewMemDB(&version.Semantic{
		Major: 1,
		Minor: 0,
		Patch: 0,
	})
	msgChan := make(chan common.Message, 1)
	vm := New(LocalAppCreator(app))
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

func mustNewCounterTestVm(t *testing.T) (*VM, Service, chan common.Message) {
	vm, _, msgChan, err := newCounterTestVM()
	require.NoError(t, err)
	require.NotNil(t, vm)

	service := NewService(vm)
	return vm, service, msgChan
}

func mustNewKVTestVm(t *testing.T) (*VM, Service, chan common.Message) {
	vm, _, msgChan, err := newKVTestVM()
	require.NoError(t, err)
	require.NotNil(t, vm)

	service := NewService(vm)
	return vm, service, msgChan
}

// MakeTxKV returns a text transaction, allong with expected key, value pair
func MakeTxKV() ([]byte, []byte, []byte) {
	k := []byte(tmrand.Str(2))
	v := []byte(tmrand.Str(2))
	return k, v, append(k, append([]byte("="), v...)...)
}

func TestInitVm(t *testing.T) {
	vm, service, msgChan := mustNewCounterTestVm(t)

	blk0, err := vm.BuildBlock(context.Background())
	assert.ErrorIs(t, err, errNoPendingTxs, "expecting error no txs")
	assert.Nil(t, blk0)

	// submit first tx (0x00)
	reply, err := service.BroadcastTxSync(nil, []byte{0x00})
	assert.NoError(t, err)
	assert.Equal(t, atypes.CodeTypeOK, reply.Code)

	select { // require there is a pending tx message to the engine
	case msg := <-msgChan:
		require.Equal(t, common.PendingTxs, msg)
	default:
		require.FailNow(t, "should have been pendingTxs message on channel")
	}

	// build 1st block
	blk1, err := vm.BuildBlock(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, blk1)

	err = blk1.Accept(context.Background())
	assert.NoError(t, err)

	tmBlk1 := blk1.(*Block).Block

	t.Logf("Block: %d", blk1.Height())
	t.Logf("TM Block Tx count: %d", len(tmBlk1.Data.Txs))

	// submit second tx (0x01)
	reply, err = service.BroadcastTxSync(nil, []byte{0x01})
	assert.NoError(t, err)
	assert.Equal(t, atypes.CodeTypeOK, reply.Code)

	// submit 3rd tx (0x02)
	reply, err = service.BroadcastTxSync(nil, []byte{0x02})
	assert.NoError(t, err)
	assert.Equal(t, atypes.CodeTypeOK, reply.Code)

	select { // require there is a pending tx message to the engine
	case msg := <-msgChan:
		require.Equal(t, common.PendingTxs, msg)
	default:
		require.FailNow(t, "should have been pendingTxs message on channel")
	}

	// build second block with 2 TX together
	blk2, err := vm.BuildBlock(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, blk2)

	err = blk2.Accept(context.Background())
	assert.NoError(t, err)

	tmBlk2 := blk2.(*Block).Block

	t.Logf("Block: %d", blk2.Height())
	t.Logf("TM Block Tx count: %d", len(tmBlk2.Data.Txs))
}

func TestRejectBlock(t *testing.T) {
	tx1 := []byte{0x01}
	tx2 := []byte{0x02}
	tx3 := []byte{0x03}
	txs := []types.Tx{tx1, tx2, tx3}
	vm, service, _ := mustNewKVTestVm(t)

	blk0, err := vm.BuildBlock(context.Background())
	assert.ErrorIs(t, err, errNoPendingTxs, "expecting error no txs")
	assert.Nil(t, blk0)

	for _, tx := range txs {
		txReply, err := service.BroadcastTxSync(&rpctypes.Context{}, tx)
		assert.NoError(t, err)
		assert.Equal(t, atypes.CodeTypeOK, txReply.Code)
	}

	// build 1st block
	blk1, err := vm.BuildBlock(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, blk1)
	assert.NoError(t, blk1.Reject(context.Background()))
	height1 := int64(blk1.Height())

	// build 1st block
	blk2, err := vm.BuildBlock(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, blk1)
	assert.NoError(t, blk1.Accept(context.Background()))
	height2 := int64(blk2.Height())

	assert.Equal(t, height1, height2)

	reply, err := service.Block(&rpctypes.Context{}, &height2)
	assert.NoError(t, err)
	if assert.NotNil(t, reply.Block) {
		assert.EqualValues(t, height2, reply.Block.Height)
	}
	assert.EqualValues(t, height2, reply.Block.Height)
	assert.EqualValues(t, len(txs), len(reply.Block.Txs))
	for _, tx := range txs {
		match := false
		for _, blkTx := range reply.Block.Txs {
			if reflect.DeepEqual(tx, blkTx) {
				match = true
				break
			}
		}
		require.True(t, match)
	}
}
