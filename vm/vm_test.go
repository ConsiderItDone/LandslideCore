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

var blockchainID = ids.ID{1, 2, 3}

func TestInitVm(t *testing.T) {
	//ctx := context.TODO()
	// Initialize the vm
	vm, _, _, err := newTestVM()
	assert.NoError(t, err)
	assert.NotNil(t, vm)

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
	err := vm.Initialize(context.TODO(), snowCtx, dbManager, []byte{0, 0, 0, 0, 0}, nil, nil, msgChan, nil, nil)
	return vm, snowCtx, msgChan, err
}
