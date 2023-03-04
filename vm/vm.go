package vm

import (
	"context"
	"fmt"
	"github.com/ava-labs/avalanchego/database/manager"
	"github.com/ava-labs/avalanchego/database/prefixdb"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/consensus/snowman"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/snow/engine/snowman/block"
	"github.com/ava-labs/avalanchego/version"
	"github.com/ava-labs/avalanchego/vms/components/chain"
	abciTypes "github.com/tendermint/tendermint/abci/types"
	cs "github.com/tendermint/tendermint/consensus"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/node"
	"github.com/tendermint/tendermint/proxy"
	sm "github.com/tendermint/tendermint/state"
	"github.com/tendermint/tendermint/store"
	"github.com/tendermint/tendermint/types"
	dbm "github.com/tendermint/tm-db"
	"time"
)

var (
	_ block.ChainVM = &VM{}
)

var (
	blockStoreDBPrefix = []byte("blockstore")
	stateDBPrefix      = []byte("state")
)

type VM struct {
	ctx       *snow.Context
	dbManager manager.Manager

	// *chain.State helps to implement the VM interface by wrapping blocks
	// with an efficient caching layer.
	*chain.State

	tmLogger log.Logger

	blockStoreDB dbm.DB
	blockStore   *store.BlockStore

	stateDB    dbm.DB
	stateStore sm.Store

	// Tendermint Application
	app abciTypes.Application

	// Tendermint proxy app
	proxyApp proxy.AppConns

	// EventBus is a common bus for all events going through the system.
	eventBus *types.EventBus
}

func NewVM(app abciTypes.Application) *VM {
	return &VM{app: app}
}

func (vm *VM) Initialize(
	_ context.Context,
	chainCtx *snow.Context,
	dbManager manager.Manager,
	genesisBytes []byte,
	upgradeBytes []byte,
	configBytes []byte,
	toEngine chan<- common.Message,
	fxs []*common.Fx,
	appSender common.AppSender,
) error {
	vm.ctx = chainCtx
	vm.tmLogger = log.NewTMLogger(vm.ctx.Log)
	vm.dbManager = dbManager

	baseDB := dbManager.Current().Database

	vm.blockStoreDB = Database{prefixdb.NewNested(blockStoreDBPrefix, baseDB)}
	vm.blockStore = store.NewBlockStore(vm.blockStoreDB)

	vm.stateDB = Database{prefixdb.NewNested(stateDBPrefix, baseDB)}
	vm.stateStore = sm.NewStore(vm.stateDB)

	// Create the proxyApp and establish connections to the ABCI app (consensus, mempool, query).
	proxyApp, err := node.CreateAndStartProxyAppConns(proxy.NewLocalClientCreator(vm.app), vm.tmLogger)
	if err != nil {
		return fmt.Errorf("failed to create and start proxy app: %w ", err)
	}
	vm.proxyApp = proxyApp

	// Create EventBus
	eventBus, err := node.CreateAndStartEventBus(vm.tmLogger)
	if err != nil {
		return fmt.Errorf("failed to create and start event bus: %w ", err)
	}
	vm.eventBus = eventBus

	//err = vm.doHandshake(vm.tmLogger.With("module", "consensus"))

	return nil
}

func (vm *VM) doHandshake(state sm.State, consensusLogger log.Logger) error {
	handshaker := cs.NewHandshaker(vm.stateStore, state, vm.blockStore, nil)
	handshaker.SetLogger(consensusLogger)
	handshaker.SetEventBus(vm.eventBus)
	if err := handshaker.Handshake(vm.proxyApp); err != nil {
		return fmt.Errorf("error during handshake: %v", err)
	}
	return nil
}

func (vm *VM) AppGossip(_ context.Context, nodeID ids.NodeID, msg []byte) error {
	return nil
}

func (vm *VM) SetState(ctx context.Context, state snow.State) error {
	//TODO implement me
	panic("implement me")
}

func (vm *VM) Shutdown(ctx context.Context) error {
	//TODO implement me
	panic("implement me")
}

func (vm *VM) Version(ctx context.Context) (string, error) {
	//TODO implement me
	panic("implement me")
}

func (vm *VM) CreateStaticHandlers(ctx context.Context) (map[string]*common.HTTPHandler, error) {
	//TODO implement me
	panic("implement me")
}

func (vm *VM) CreateHandlers(ctx context.Context) (map[string]*common.HTTPHandler, error) {
	//TODO implement me
	panic("implement me")
}

func (vm *VM) GetBlock(ctx context.Context, blkID ids.ID) (snowman.Block, error) {
	//TODO implement me
	panic("implement me")
}

func (vm *VM) ParseBlock(ctx context.Context, blockBytes []byte) (snowman.Block, error) {
	//TODO implement me
	panic("implement me")
}

func (vm *VM) BuildBlock(ctx context.Context) (snowman.Block, error) {
	//TODO implement me
	panic("implement me")
}

func (vm *VM) SetPreference(ctx context.Context, blkID ids.ID) error {
	//TODO implement me
	panic("implement me")
}

//func (vm *VM) LastAccepted(ctx context.Context) (ids.ID, error) {
//	//TODO implement me
//	panic("implement me")
//}

func (vm *VM) AppRequest(_ context.Context, nodeID ids.NodeID, requestID uint32, time time.Time, request []byte) error {
	return nil
}

// This VM doesn't (currently) have any app-specific messages
func (vm *VM) AppResponse(_ context.Context, nodeID ids.NodeID, requestID uint32, response []byte) error {
	return nil
}

// This VM doesn't (currently) have any app-specific messages
func (vm *VM) AppRequestFailed(_ context.Context, nodeID ids.NodeID, requestID uint32) error {
	return nil
}

func (vm *VM) CrossChainAppRequest(_ context.Context, _ ids.ID, _ uint32, deadline time.Time, request []byte) error {
	return nil
}

func (vm *VM) CrossChainAppRequestFailed(_ context.Context, _ ids.ID, _ uint32) error {
	return nil
}

func (vm *VM) CrossChainAppResponse(_ context.Context, _ ids.ID, _ uint32, response []byte) error {
	return nil
}

func (vm *VM) Connected(_ context.Context, id ids.NodeID, nodeVersion *version.Application) error {
	return nil // noop
}

func (vm *VM) Disconnected(_ context.Context, id ids.NodeID) error {
	return nil // noop
}

func (vm *VM) HealthCheck(ctx context.Context) (interface{}, error) { return nil, nil }
