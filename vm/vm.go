package vm

import (
	"context"
	"errors"
	"fmt"
	avalanchegoMetrics "github.com/ava-labs/avalanchego/api/metrics"
	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/database/manager"
	"github.com/ava-labs/avalanchego/database/prefixdb"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/choices"
	"github.com/ava-labs/avalanchego/snow/consensus/snowman"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/snow/engine/snowman/block"
	"github.com/ava-labs/avalanchego/version"
	"github.com/ava-labs/avalanchego/vms/components/chain"
	"github.com/gorilla/rpc/v2"
	"github.com/prometheus/client_golang/prometheus"
	abciTypes "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/config"
	cs "github.com/tendermint/tendermint/consensus"
	"github.com/tendermint/tendermint/libs/log"
	mempl "github.com/tendermint/tendermint/mempool"
	"github.com/tendermint/tendermint/node"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/tendermint/tendermint/proxy"
	rpccore "github.com/tendermint/tendermint/rpc/core"
	rpcserver "github.com/tendermint/tendermint/rpc/jsonrpc/server"
	sm "github.com/tendermint/tendermint/state"
	"github.com/tendermint/tendermint/store"
	"github.com/tendermint/tendermint/types"
	dbm "github.com/tendermint/tm-db"
	"net/http"
	"time"
)

var (
	_ block.ChainVM = &VM{}
)

const (
	Name = "LandslideCore"

	decidedCacheSize    = 100
	missingCacheSize    = 50
	unverifiedCacheSize = 50
)

var (
	chainStateMetricsPrefix = "chain_state"

	lastAcceptedKey    = []byte("last_accepted_key")
	blockStoreDBPrefix = []byte("blockstore")
	stateDBPrefix      = []byte("state")
)

var (
	errInvalidBlock = errors.New("invalid block")
	errNoPendingTxs = errors.New("there is no txs to include to block")
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
	tmState    *sm.State

	mempool mempl.Mempool

	// Tendermint Application
	app abciTypes.Application

	// Tendermint proxy app
	proxyApp proxy.AppConns

	// EventBus is a common bus for all events going through the system.
	eventBus *types.EventBus

	// [acceptedBlockDB] is the database to store the last accepted
	// block.
	acceptedBlockDB database.Database

	genesis *types.GenesisDoc

	// Metrics
	multiGatherer avalanchegoMetrics.MultiGatherer
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

	err := vm.initGenesis(genesisBytes)
	if err != nil {
		return err
	}

	state, err := vm.stateStore.LoadFromDBOrGenesisDoc(vm.genesis)
	if err != nil {
		return fmt.Errorf("failed to load tmState from genesis: %w ", err)
	}
	vm.tmState = &state

	// genesis only
	if vm.tmState.LastBlockHeight == 0 {
		// TODO use decoded/encoded genesis bytes
		block, partSet := vm.tmState.MakeBlock(1, []types.Tx{genesisBytes}, nil, nil, nil)
		vm.tmLogger.Info("init block", "b", block, "part set", partSet)
	}

	//vm.genesisHash = vm.ethConfig.Genesis.ToBlock(nil).Hash() // must create genesis hash before [vm.readLastAccepted]

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

	err = vm.doHandshake(vm.genesis, vm.tmLogger.With("module", "consensus"))
	if err != nil {
		return err
	}
	state, err = vm.stateStore.Load()
	if err != nil {
		return fmt.Errorf("failed to load tmState: %w ", err)
	}
	vm.tmState = &state

	vm.mempool = vm.createMempool()

	if err := vm.initializeMetrics(); err != nil {
		return err
	}

	if err := vm.initChainState(nil); err != nil {
		return err
	}

	return nil
}

// Initializes Genesis if required
func (vm *VM) initGenesis(genesisData []byte) error {
	// load genesis from database
	genesis, err := node.LoadGenesisDoc(vm.stateDB)
	// genesis not found in database
	if err != nil {
		if err == node.ErrNoGenesisDoc {
			// get it from json
			genesis, err = types.GenesisDocFromJSON(genesisData)
			if err != nil {
				return fmt.Errorf("failed to decode genesis bytes: %w ", err)
			}
			// save to database
			err = node.SaveGenesisDoc(vm.stateDB, genesis)
			if err != nil {
				return fmt.Errorf("failed to save genesis data: %w ", err)
			}

			// store genesis as first block

		} else {
			return err
		}
	}

	vm.genesis = genesis
	return nil
}

func (vm *VM) createMempool() *mempl.CListMempool {
	cfg := config.DefaultMempoolConfig()
	mempool := mempl.NewCListMempool(
		cfg,
		vm.proxyApp.Mempool(),
		vm.tmState.LastBlockHeight,
		mempl.WithMetrics(mempl.NopMetrics()), // TODO: use prometheus metrics based on config
		mempl.WithPreCheck(sm.TxPreCheck(*vm.tmState)),
		mempl.WithPostCheck(sm.TxPostCheck(*vm.tmState)),
	)
	mempoolLogger := vm.tmLogger.With("module", "mempool")
	mempool.SetLogger(mempoolLogger)

	return mempool
}

func (vm *VM) doHandshake(genesis *types.GenesisDoc, consensusLogger log.Logger) error {
	handshaker := cs.NewHandshaker(vm.stateStore, *vm.tmState, vm.blockStore, genesis)
	handshaker.SetLogger(consensusLogger)
	handshaker.SetEventBus(vm.eventBus)
	if err := handshaker.Handshake(vm.proxyApp); err != nil {
		return fmt.Errorf("error during handshake: %v", err)
	}
	return nil
}

// readLastAccepted reads the last accepted hash from [acceptedBlockDB] and returns the
// last accepted block hash and height by reading directly from [vm.chaindb] instead of relying
// on [chain].
// Note: assumes chaindb, ethConfig, and genesisHash have been initialized.
//func (vm *VM) readLastAccepted() (tmbytes.HexBytes, uint64, error) {
//	// Attempt to load last accepted block to determine if it is necessary to
//	// initialize state with the genesis block.
//	lastAcceptedBytes, lastAcceptedErr := vm.acceptedBlockDB.Get(lastAcceptedKey)
//	switch {
//	case lastAcceptedErr == database.ErrNotFound:
//		// If there is nothing in the database, return the genesis block hash and height
//		return vm.genesisHash, 0, nil
//	case lastAcceptedErr != nil:
//		return common.Hash{}, 0, fmt.Errorf("failed to get last accepted block ID due to: %w", lastAcceptedErr)
//	case len(lastAcceptedBytes) != common.HashLength:
//		return common.Hash{}, 0, fmt.Errorf("last accepted bytes should have been length %d, but found %d", common.HashLength, len(lastAcceptedBytes))
//	default:
//		lastAcceptedHash := common.BytesToHash(lastAcceptedBytes)
//		height := rawdb.ReadHeaderNumber(vm.chaindb, lastAcceptedHash)
//		if height == nil {
//			return common.Hash{}, 0, fmt.Errorf("failed to retrieve header number of last accepted block: %s", lastAcceptedHash)
//		}
//		return lastAcceptedHash, *height, nil
//	}
//}

func (vm *VM) initChainState(lastAcceptedBlock *types.Block) error {
	block, err := vm.newBlock(lastAcceptedBlock)
	if err != nil {
		return fmt.Errorf("failed to create block wrapper for the last accepted block: %w", err)
	}
	block.status = choices.Accepted

	config := &chain.Config{
		DecidedCacheSize:    decidedCacheSize,
		MissingCacheSize:    missingCacheSize,
		UnverifiedCacheSize: unverifiedCacheSize,
		//GetBlockIDAtHeight:  vm.GetBlockIDAtHeight,
		GetBlock:          vm.getBlock,
		UnmarshalBlock:    vm.parseBlock,
		BuildBlock:        vm.buildBlock,
		LastAcceptedBlock: block,
	}

	// Register chain state metrics
	chainStateRegisterer := prometheus.NewRegistry()
	state, err := chain.NewMeteredState(chainStateRegisterer, config)
	if err != nil {
		return fmt.Errorf("could not create metered state: %w", err)
	}
	vm.State = state

	return vm.multiGatherer.Register(chainStateMetricsPrefix, chainStateRegisterer)
}

func (vm *VM) initializeMetrics() error {
	vm.multiGatherer = avalanchegoMetrics.NewMultiGatherer()

	if err := vm.ctx.Metrics.Register(vm.multiGatherer); err != nil {
		return err
	}

	return nil
}

// parseBlock parses [b] into a block to be wrapped by ChainState.
func (vm *VM) parseBlock(_ context.Context, b []byte) (snowman.Block, error) {
	protoBlock := new(tmproto.Block)
	err := protoBlock.Unmarshal(b)
	if err != nil {
		return nil, err
	}

	tmBlock, err := types.BlockFromProto(protoBlock)
	if err != nil {
		return nil, err
	}

	// Note: the status of block is set by ChainState
	block, err := vm.newBlock(tmBlock)
	if err != nil {
		return nil, err
	}

	return block, nil
}

// getBlock attempts to retrieve block [id] from the VM to be wrapped
// by ChainState.
func (vm *VM) getBlock(_ context.Context, id ids.ID) (snowman.Block, error) {
	var hash []byte
	copy(hash, id[:])
	tmBlock := vm.blockStore.LoadBlockByHash(hash)
	// If [tmBlock] is nil, return [database.ErrNotFound] here
	// so that the miss is considered cacheable.
	if tmBlock == nil {
		return nil, database.ErrNotFound
	}
	// Note: the status of block is set by ChainState
	return vm.newBlock(tmBlock)
}

// buildBlock builds a block to be wrapped by ChainState
func (vm *VM) buildBlock(_ context.Context) (snowman.Block, error) {
	txs := vm.mempool.ReapMaxBytesMaxGas(-1, -1)
	if len(txs) == 0 {
		return nil, errNoPendingTxs
	}
	height := vm.tmState.LastBlockHeight + 1
	block, _ := vm.tmState.MakeBlock(height, txs, nil, nil, nil)

	// Note: the status of block is set by ChainState
	blk, err := vm.newBlock(block)
	blk.SetStatus(choices.Processing)
	if err != nil {
		return nil, err
	}
	vm.tmLogger.Debug(fmt.Sprintf("Built block %s", blk.ID()))

	return blk, nil
}

func (vm *VM) AppGossip(_ context.Context, nodeID ids.NodeID, msg []byte) error {
	return nil
}

func (vm *VM) SetState(ctx context.Context, state snow.State) error {
	return nil
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
	mux := http.NewServeMux()
	rpcLogger := vm.tmLogger.With("module", "rpc-server")
	rpcserver.RegisterRPCFuncs(mux, rpccore.Routes, rpcLogger)

	server := rpc.NewServer()
	//server.RegisterCodec(json.NewCodec(), "application/json")
	//server.RegisterCodec(json.NewCodec(), "application/json;charset=UTF-8")
	if err := server.RegisterService(&Service{vm: vm}, Name); err != nil {
		return nil, err
	}

	return map[string]*common.HTTPHandler{
		"": {
			LockOptions: common.WriteLock,
			Handler:     mux,
		},
	}, nil
}

func (vm *VM) ParseBlock(ctx context.Context, blockBytes []byte) (snowman.Block, error) {
	//TODO implement me
	panic("implement me")
}

func (vm *VM) SetPreference(ctx context.Context, blkID ids.ID) error {
	return nil
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
