package vm

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/ava-labs/avalanchego/database/manager"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/version"
	"github.com/ava-labs/avalanchego/vms/components/chain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tendermint/tendermint/abci/example/counter"
	"github.com/tendermint/tendermint/abci/types"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"

	_ "embed"
)

var (
	blockchainID = ids.ID{1, 2, 3}

	//go:embed data/vm_test_genesis.json
	genesis string
)

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

func mustNewTestVm(t *testing.T) (*VM, Service) {
	vm, _, _, err := newTestVM()
	require.NoError(t, err)
	require.NotNil(t, vm)
	service := NewService(vm)
	return vm, service
}

func TestBroadcastTxSync(t *testing.T) {
	vm, service := mustNewTestVm(t)

	testData := [][]byte{nil, {0x00}, {0x01}, {0x02}}
	for i := range testData {
		args := &BroadcastTxArgs{testData[i]}
		t.Run(fmt.Sprintf("iteration %d", i), func(t *testing.T) {
			reply := new(ctypes.ResultBroadcastTx)
			if args.Tx != nil {
				err := service.BroadcastTxSync(nil, args, reply)
				assert.NoError(t, err)
				assert.Equal(t, types.CodeTypeOK, reply.Code)
			}

			blk, err := vm.BuildBlock(context.Background())
			if args.Tx == nil {
				assert.ErrorIs(t, err, errNoPendingTxs, "expecting error no txs")
				assert.Nil(t, blk)

				t.Log("Block: skipped")
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, blk)
				assert.NoError(t, blk.Accept(context.Background()))

				tmBlk := blk.(*chain.BlockWrapper).Block.(*Block).tmBlock
				t.Logf("Height: %d, Txs: %d", blk.Height(), len(tmBlk.Data.Txs))
			}
		})
	}
}
