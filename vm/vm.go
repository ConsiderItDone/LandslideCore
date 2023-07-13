package vm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/rpc/v2"

	"github.com/ava-labs/avalanchego/api/health"
	"github.com/ava-labs/avalanchego/api/metrics"
	"github.com/ava-labs/avalanchego/database/manager"
	"github.com/ava-labs/avalanchego/database/prefixdb"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/choices"
	"github.com/ava-labs/avalanchego/snow/consensus/snowman"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/snow/engine/snowman/block"
	"github.com/ava-labs/avalanchego/snow/validators"
	"github.com/ava-labs/avalanchego/utils"
	"github.com/ava-labs/avalanchego/utils/json"
	"github.com/ava-labs/avalanchego/version"

	abciTypes "github.com/consideritdone/landslidecore/abci/types"
	"github.com/consideritdone/landslidecore/config"
	"github.com/consideritdone/landslidecore/consensus"
	"github.com/consideritdone/landslidecore/crypto/tmhash"
	"github.com/consideritdone/landslidecore/libs/log"
	mempl "github.com/consideritdone/landslidecore/mempool"
	"github.com/consideritdone/landslidecore/node"
	tmstate "github.com/consideritdone/landslidecore/proto/tendermint/state"
	tmproto "github.com/consideritdone/landslidecore/proto/tendermint/types"
	"github.com/consideritdone/landslidecore/proxy"
	rpccore "github.com/consideritdone/landslidecore/rpc/core"
	rpcserver "github.com/consideritdone/landslidecore/rpc/jsonrpc/server"
	"github.com/consideritdone/landslidecore/state"
	"github.com/consideritdone/landslidecore/state/indexer"
	blockidxkv "github.com/consideritdone/landslidecore/state/indexer/block/kv"
	"github.com/consideritdone/landslidecore/state/txindex"
	txidxkv "github.com/consideritdone/landslidecore/state/txindex/kv"
	"github.com/consideritdone/landslidecore/store"
	"github.com/consideritdone/landslidecore/types"
)

const (
	Name = "landslide"

	decidedCacheSize    = 100
	missingCacheSize    = 50
	unverifiedCacheSize = 50

	genesisChunkSize = 16 * 1024 * 1024 // 16
)

var (
	Version = version.Semantic{
		Major: 0,
		Minor: 1,
		Patch: 2,
	}
	_ common.NetworkAppHandler    = (*VM)(nil)
	_ common.CrossChainAppHandler = (*VM)(nil)
	_ common.AppHandler           = (*VM)(nil)
	_ health.Checker              = (*VM)(nil)
	_ validators.Connector        = (*VM)(nil)
	_ common.VM                   = (*VM)(nil)
	_ block.Getter                = (*VM)(nil)
	_ block.Parser                = (*VM)(nil)
	_ block.ChainVM               = (*VM)(nil)

	dbPrefixBlockStore   = []byte("block-store")
	dbPrefixStateStore   = []byte("state-store")
	dbPrefixTxIndexer    = []byte("tx-indexer")
	dbPrefixBlockIndexer = []byte("block-indexer")

	proposerAddress = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}

	errInvalidBlock = errors.New("invalid block")
	errNoPendingTxs = errors.New("there is no txs to include to block")
)

type (
	AppCreator func(ids.ID) (abciTypes.Application, error)

	VM struct {
		appCreator AppCreator
		app        proxy.AppConns

		log      log.Logger
		chainCtx *snow.Context
		toEngine chan<- common.Message

		blockStore *store.BlockStore
		stateStore state.Store
		state      state.State
		genesis    *types.GenesisDoc

		mempool  *mempl.CListMempool
		eventBus *types.EventBus

		txIndexer      txindex.TxIndexer
		blockIndexer   indexer.BlockIndexer
		indexerService *txindex.IndexerService
		multiGatherer  metrics.MultiGatherer

		bootstrapped   utils.Atomic[bool]
		verifiedBlocks map[ids.ID]*Block
		preferred      ids.ID
	}
)

func LocalAppCreator(app abciTypes.Application) AppCreator {
	return func(ids.ID) (abciTypes.Application, error) {
		return app, nil
	}
}

func New(appCreator AppCreator) *VM {
	return &VM{
		appCreator: appCreator,
		app:        nil,
	}
}

// Notify this engine of a request for data from [nodeID].
//
// The meaning of [request], and what should be sent in response to it, is
// application (VM) specific.
//
// It is not guaranteed that:
// * [request] is well-formed/valid.
//
// This node should typically send an AppResponse to [nodeID] in response to
// a valid message using the same request ID before the deadline. However,
// the VM may arbitrarily choose to not send a response to this request.
func (vm *VM) AppRequest(ctx context.Context, nodeID ids.NodeID, requestID uint32, deadline time.Time, request []byte) error {
	panic("implement me")
}

// Notify this engine that an AppRequest message it sent to [nodeID] with
// request ID [requestID] failed.
//
// This may be because the request timed out or because the message couldn't
// be sent to [nodeID].
//
// It is guaranteed that:
// * This engine sent a request to [nodeID] with ID [requestID].
// * AppRequestFailed([nodeID], [requestID]) has not already been called.
// * AppResponse([nodeID], [requestID]) has not already been called.
func (vm *VM) AppRequestFailed(ctx context.Context, nodeID ids.NodeID, requestID uint32) error {
	panic("implement me")
}

// Notify this engine of a response to the AppRequest message it sent to
// [nodeID] with request ID [requestID].
//
// The meaning of [response] is application (VM) specifc.
//
// It is guaranteed that:
// * This engine sent a request to [nodeID] with ID [requestID].
// * AppRequestFailed([nodeID], [requestID]) has not already been called.
// * AppResponse([nodeID], [requestID]) has not already been called.
//
// It is not guaranteed that:
// * [response] contains the expected response
// * [response] is well-formed/valid.
//
// If [response] is invalid or not the expected response, the VM chooses how
// to react. For example, the VM may send another AppRequest, or it may give
// up trying to get the requested information.
func (vm *VM) AppResponse(ctx context.Context, nodeID ids.NodeID, requestID uint32, response []byte) error {
	panic("implement me")
}

// Notify this engine of a gossip message from [nodeID].
//
// The meaning of [msg] is application (VM) specific, and the VM defines how
// to react to this message.
//
// This message is not expected in response to any event, and it does not
// need to be responded to.
//
// A node may gossip the same message multiple times. That is,
// AppGossip([nodeID], [msg]) may be called multiple times.
func (vm *VM) AppGossip(ctx context.Context, nodeID ids.NodeID, msg []byte) error {
	panic("implement me")
}

// CrossChainAppRequest Notify this engine of a request for data from
// [chainID].
//
// The meaning of [request], and what should be sent in response to it, is
// application (VM) specific.
//
// Guarantees surrounding the request are specific to the implementation of
// the requesting VM. For example, the request may or may not be guaranteed
// to be well-formed/valid depending on the implementation of the requesting
// VM.
//
// This node should typically send a CrossChainAppResponse to [chainID] in
// response to a valid message using the same request ID before the
// deadline. However, the VM may arbitrarily choose to not send a response
// to this request.
func (vm *VM) CrossChainAppRequest(ctx context.Context, chainID ids.ID, requestID uint32, deadline time.Time, request []byte) error {
	panic("implement me")
}

// CrossChainAppRequestFailed notifies this engine that a
// CrossChainAppRequest message it sent to [chainID] with request ID
// [requestID] failed.
//
// This may be because the request timed out or because the message couldn't
// be sent to [chainID].
//
// It is guaranteed that:
// * This engine sent a request to [chainID] with ID [requestID].
// * CrossChainAppRequestFailed([chainID], [requestID]) has not already been
// called.
// * CrossChainAppResponse([chainID], [requestID]) has not already been
// called.
func (vm *VM) CrossChainAppRequestFailed(ctx context.Context, chainID ids.ID, requestID uint32) error {
	panic("implement me")
}

// CrossChainAppResponse notifies this engine of a response to the
// CrossChainAppRequest message it sent to [chainID] with request ID
// [requestID].
//
// The meaning of [response] is application (VM) specific.
//
// It is guaranteed that:
// * This engine sent a request to [chainID] with ID [requestID].
// * CrossChainAppRequestFailed([chainID], [requestID]) has not already been
// called.
// * CrossChainAppResponse([chainID], [requestID]) has not already been
// called.
//
// Guarantees surrounding the response are specific to the implementation of
// the responding VM. For example, the response may or may not be guaranteed
// to be well-formed/valid depending on the implementation of the requesting
// VM.
//
// If [response] is invalid or not the expected response, the VM chooses how
// to react. For example, the VM may send another CrossChainAppRequest, or
// it may give up trying to get the requested information.
func (vm *VM) CrossChainAppResponse(ctx context.Context, chainID ids.ID, requestID uint32, response []byte) error {
	panic("implement me")
}

// HealthCheck returns health check results and, if not healthy, a non-nil
// error
//
// It is expected that the results are json marshallable.
func (vm *VM) HealthCheck(context.Context) (interface{}, error) {
	return nil, nil
}

// Connector represents a handler that is called when a connection is marked as connected
func (vm *VM) Connected(ctx context.Context, nodeID ids.NodeID, nodeVersion *version.Application) error {
	vm.log.Info("connected", "nodeID", nodeID.String(), "nodeVersion", nodeVersion.String())
	return nil
}

// Connector represents a handler that is called when a connection is marked as disconnected
func (vm *VM) Disconnected(ctx context.Context, nodeID ids.NodeID) error {
	vm.log.Info("disconnected", "nodeID", nodeID.String())
	return nil
}

// Initialize this VM.
// [chainCtx]: Metadata about this VM.
//
//	[chainCtx.networkID]: The ID of the network this VM's chain is
//	                      running on.
//	[chainCtx.chainID]: The unique ID of the chain this VM is running on.
//	[chainCtx.Log]: Used to log messages
//	[chainCtx.NodeID]: The unique staker ID of this node.
//	[chainCtx.Lock]: A Read/Write lock shared by this VM and the
//	                 consensus engine that manages this VM. The write
//	                 lock is held whenever code in the consensus engine
//	                 calls the VM.
//
// [dbManager]: The manager of the database this VM will persist data to.
// [genesisBytes]: The byte-encoding of the genesis information of this
//
//	VM. The VM uses it to initialize its state. For
//	example, if this VM were an account-based payments
//	system, `genesisBytes` would probably contain a genesis
//	transaction that gives coins to some accounts, and this
//	transaction would be in the genesis block.
//
// [toEngine]: The channel used to send messages to the consensus engine.
// [fxs]: Feature extensions that attach to this VM.
func (vm *VM) Initialize(
	ctx context.Context,
	chainCtx *snow.Context,
	dbManager manager.Manager,
	genesisBytes []byte,
	upgradeBytes []byte,
	configBytes []byte,
	toEngine chan<- common.Message,
	fxs []*common.Fx,
	appSender common.AppSender,
) error {
	vm.chainCtx = chainCtx
	vm.toEngine = toEngine
	vm.log = log.NewTMLogger(vm.chainCtx.Log).With("module", "vm")
	vm.verifiedBlocks = make(map[ids.ID]*Block)

	db := dbManager.Current().Database

	dbBlockStore := NewDB(prefixdb.NewNested(dbPrefixBlockStore, db))
	vm.blockStore = store.NewBlockStore(dbBlockStore)

	dbStateStore := NewDB(prefixdb.NewNested(dbPrefixStateStore, db))
	vm.stateStore = state.NewStore(dbStateStore)

	app, err := vm.appCreator(chainCtx.ChainID)
	if err != nil {
		return err
	}

	vm.state, vm.genesis, err = node.LoadStateFromDBOrGenesisDocProvider(
		dbStateStore,
		NewLocalGenesisDocProvider(genesisBytes),
	)
	if err != nil {
		return nil
	}

	vm.app, err = node.CreateAndStartProxyAppConns(proxy.NewLocalClientCreator(app), vm.log)
	if err != nil {
		return err
	}

	vm.eventBus, err = node.CreateAndStartEventBus(vm.log)
	if err != nil {
		return err
	}

	dbTxIndexer := NewDB(prefixdb.NewNested(dbPrefixTxIndexer, db))
	vm.txIndexer = txidxkv.NewTxIndex(dbTxIndexer)

	dbBlockIndexer := NewDB(prefixdb.NewNested(dbPrefixBlockIndexer, db))
	vm.blockIndexer = blockidxkv.New(dbBlockIndexer)

	vm.indexerService = txindex.NewIndexerService(vm.txIndexer, vm.blockIndexer, vm.eventBus)
	vm.indexerService.SetLogger(vm.log.With("module", "indexer"))
	if err := vm.indexerService.Start(); err != nil {
		return err
	}

	handshaker := consensus.NewHandshaker(
		vm.stateStore,
		vm.state,
		vm.blockStore,
		vm.genesis,
	)
	handshaker.SetLogger(vm.log.With("module", "consensus"))
	handshaker.SetEventBus(vm.eventBus)
	if err := handshaker.Handshake(vm.app); err != nil {
		return fmt.Errorf("error during handshake: %v", err)
	}

	vm.state, err = vm.stateStore.Load()
	if err != nil {
		return nil
	}

	vm.mempool = mempl.NewCListMempool(
		config.DefaultMempoolConfig(),
		vm.app.Mempool(),
		vm.state.LastBlockHeight,
		vm,
		mempl.WithMetrics(mempl.NopMetrics()),
		mempl.WithPreCheck(state.TxPreCheck(vm.state)),
		mempl.WithPostCheck(state.TxPostCheck(vm.state)),
	)
	vm.mempool.SetLogger(vm.log.With("module", "mempool"))
	vm.mempool.EnableTxsAvailable()

	vm.multiGatherer = metrics.NewMultiGatherer()
	if err := vm.chainCtx.Metrics.Register(vm.multiGatherer); err != nil {
		return err
	}

	if vm.state.LastBlockHeight == 0 {
		block, _ := vm.state.MakeBlock(1, types.Txs{types.Tx(genesisBytes)}, makeCommitMock(1, time.Now()), nil, proposerAddress)
		block.LastBlockID = types.BlockID{
			Hash: tmhash.Sum([]byte{}),
			PartSetHeader: types.PartSetHeader{
				Total: 0,
				Hash:  tmhash.Sum([]byte{}),
			},
		}
		if err := NewBlock(vm, block, choices.Processing).Accept(ctx); err != nil {
			return err
		}
	}

	vm.log.Info("vm initialization completed")
	return nil
}

func (vm *VM) NotifyBlockReady() {
	select {
	case vm.toEngine <- common.PendingTxs:
		vm.log.Debug("notify consensys engine")
	default:
		vm.log.Error("failed to push PendingTxs notification to the consensus engine.")
	}
}

// SetState communicates to VM its next state it starts
func (vm *VM) SetState(ctx context.Context, state snow.State) error {
	vm.log.Debug("set state", "state", state.String())
	switch state {
	case snow.Bootstrapping:
		vm.bootstrapped.Set(false)
	case snow.NormalOp:
		vm.bootstrapped.Set(true)
	default:
		return snow.ErrUnknownState
	}
	return nil
}

// Shutdown is called when the node is shutting down.
func (vm *VM) Shutdown(context.Context) error {
	vm.log.Debug("shutdown start")

	if err := vm.indexerService.Stop(); err != nil {
		return fmt.Errorf("error closing indexerService: %w ", err)
	}

	if err := vm.eventBus.Stop(); err != nil {
		return fmt.Errorf("error closing eventBus: %w ", err)
	}

	if err := vm.app.Stop(); err != nil {
		return fmt.Errorf("error closing app: %w ", err)
	}

	if err := vm.stateStore.Close(); err != nil {
		return fmt.Errorf("error closing stateStore: %w ", err)
	}

	if err := vm.blockStore.Close(); err != nil {
		return fmt.Errorf("Error closing blockStore: %w ", err)
	}

	vm.log.Debug("shutdown completed")
	return nil
}

// Version returns the version of the VM.
func (vm *VM) Version(context.Context) (string, error) {
	return Version.String(), nil
}

// Creates the HTTP handlers for custom VM network calls.
//
// This exposes handlers that the outside world can use to communicate with
// a static reference to the VM. Each handler has the path:
// [Address of node]/ext/VM/[VM ID]/[extension]
//
// Returns a mapping from [extension]s to HTTP handlers.
//
// Each extension can specify how locking is managed for convenience.
//
// For example, it might make sense to have an extension for creating
// genesis bytes this VM can interpret.
//
// Note: If this method is called, no other method will be called on this VM.
// Each registered VM will have a single instance created to handle static
// APIs. This instance will be handled separately from instances created to
// service an instance of a chain.
func (vm *VM) CreateStaticHandlers(context.Context) (map[string]*common.HTTPHandler, error) {
	// ToDo: need to add implementation
	return nil, nil
}

// Creates the HTTP handlers for custom chain network calls.
//
// This exposes handlers that the outside world can use to communicate with
// the chain. Each handler has the path:
// [Address of node]/ext/bc/[chain ID]/[extension]
//
// Returns a mapping from [extension]s to HTTP handlers.
//
// Each extension can specify how locking is managed for convenience.
//
// For example, if this VM implements an account-based payments system,
// it have an extension called `accounts`, where clients could get
// information about their accounts.
func (vm *VM) CreateHandlers(context.Context) (map[string]*common.HTTPHandler, error) {
	mux := http.NewServeMux()
	rpcLogger := vm.log.With("module", "rpc-server")
	rpcserver.RegisterRPCFuncs(mux, rpccore.Routes, rpcLogger)

	server := rpc.NewServer()
	server.RegisterCodec(json.NewCodec(), "application/json")
	server.RegisterCodec(json.NewCodec(), "application/json;charset=UTF-8")
	if err := server.RegisterService(NewService(vm), Name); err != nil {
		return nil, err
	}

	return map[string]*common.HTTPHandler{
		"/rpc": {
			LockOptions: common.WriteLock,
			Handler:     server,
		},
	}, nil
}

// Attempt to load a block.
//
// If the block does not exist, database.ErrNotFound should be returned.
//
// It is expected that blocks that have been successfully verified should be
// returned correctly. It is also expected that blocks that have been
// accepted by the consensus engine should be able to be fetched. It is not
// required for blocks that have been rejected by the consensus engine to be
// able to be fetched.
func (vm *VM) GetBlock(ctx context.Context, blkID ids.ID) (snowman.Block, error) {
	vm.log.Debug("get block", "blkID", blkID.String())
	if b, ok := vm.verifiedBlocks[blkID]; ok {
		vm.log.Debug("get block", "status", b.Status())
		return b, nil
	}
	b := vm.blockStore.LoadBlockByHash(blkID[:])
	if b == nil {
		return nil, errInvalidBlock
	}
	vm.log.Debug("get block", "status", choices.Accepted)
	return NewBlock(vm, b, choices.Accepted), nil
}

// Attempt to create a block from a stream of bytes.
//
// The block should be represented by the full byte array, without extra
// bytes.
//
// It is expected for all historical blocks to be parseable.
func (vm *VM) ParseBlock(ctx context.Context, blockBytes []byte) (snowman.Block, error) {
	vm.log.Debug("parse block")

	protoBlock := new(tmproto.Block)
	if err := protoBlock.Unmarshal(blockBytes[1:]); err != nil {
		vm.log.Error("can't parse block", "err", err)
		return nil, err
	}

	block, err := types.BlockFromProto(protoBlock)
	if err != nil {
		vm.log.Error("can't create block from proto", "err", err)
		return nil, err
	}

	blk := NewBlock(vm, block, choices.Status(uint32(blockBytes[0])))
	vm.log.Debug("parsed block", "id", blk.ID(), "status", blk.Status().String())
	if _, ok := vm.verifiedBlocks[blk.ID()]; !ok {
		vm.verifiedBlocks[blk.ID()] = blk
	}

	return blk, nil
}

// Attempt to create a new block from data contained in the VM.
//
// If the VM doesn't want to issue a new block, an error should be
// returned.
func (vm *VM) BuildBlock(ctx context.Context) (snowman.Block, error) {
	vm.log.Debug("build block")

	txs := vm.mempool.ReapMaxBytesMaxGas(-1, -1)
	if len(txs) == 0 {
		return nil, errNoPendingTxs
	}

	state := vm.state.Copy()

	var preferredBlock *types.Block
	if vm.preferred != ids.Empty {
		if b, ok := vm.verifiedBlocks[vm.preferred]; ok {
			vm.log.Debug("load preferred block from cache", "id", vm.preferred.String())
			preferredBlock = b.Block
		} else {
			vm.log.Debug("load preferred block from blockStore", "id", vm.preferred.String())
			preferredBlock = vm.blockStore.LoadBlockByHash(vm.preferred[:])
			if preferredBlock == nil {
				return nil, errInvalidBlock
			}
		}
	} else {
		preferredBlock = vm.blockStore.LoadBlockByHash(state.LastBlockID.Hash)
	}
	preferredHeight := preferredBlock.Header.Height

	commit := makeCommitMock(preferredHeight+1, time.Now())
	block, _ := state.MakeBlock(preferredHeight+1, txs, commit, nil, proposerAddress)
	block.LastBlockID = types.BlockID{
		Hash:          preferredBlock.Hash(),
		PartSetHeader: preferredBlock.MakePartSet(types.BlockPartSizeBytes).Header(),
	}

	blk := NewBlock(vm, block, choices.Processing)
	vm.verifiedBlocks[blk.ID()] = blk

	vm.log.Debug("build block", "id", blk.ID(), "height", blk.Height(), "txs", len(block.Txs))
	return blk, nil
}

// Notify the VM of the currently preferred block.
//
// This should always be a block that has no children known to consensus.
func (vm *VM) SetPreference(ctx context.Context, blkID ids.ID) error {
	vm.log.Debug("set preference", "blkID", blkID.String())
	vm.preferred = blkID
	return nil
}

// LastAccepted returns the ID of the last accepted block.
//
// If no blocks have been accepted by consensus yet, it is assumed there is
// a definitionally accepted block, the Genesis block, that will be
// returned.
func (vm *VM) LastAccepted(context.Context) (ids.ID, error) {
	if vm.preferred == ids.Empty {
		return ids.ID(vm.state.LastBlockID.Hash), nil
	}
	return vm.preferred, nil
}

func (vm *VM) applyBlock(block *Block) error {
	vm.mempool.Lock()
	defer vm.mempool.Unlock()

	state := vm.state.Copy()

	err := validateBlock(state, block.Block)
	if err != nil {
		return err
	}

	abciResponses := new(tmstate.ABCIResponses)
	if state.LastBlockHeight > 0 {
		abciResponses, err = execBlockOnProxyApp(vm.log, vm.app.Consensus(), block.Block, vm.stateStore, state.InitialHeight)
		if err != nil {
			return err
		}
	} else {
		abciResponses.DeliverTxs = []*abciTypes.ResponseDeliverTx{
			&abciTypes.ResponseDeliverTx{
				Code: abciTypes.CodeTypeOK,
				Data: block.Txs[0],
			},
		}
		abciResponses.BeginBlock = new(abciTypes.ResponseBeginBlock)
		abciResponses.EndBlock = new(abciTypes.ResponseEndBlock)
	}

	// Save the results before we commit.
	if err := vm.stateStore.SaveABCIResponses(block.Block.Height, abciResponses); err != nil {
		return err
	}

	blockID := types.BlockID{
		Hash:          block.Block.Hash(),
		PartSetHeader: block.Block.MakePartSet(types.BlockPartSizeBytes).Header(),
	}

	// while mempool is Locked, flush to ensure all async requests have completed
	// in the ABCI app before Commit.
	if err := vm.mempool.FlushAppConn(); err != nil {
		vm.log.Error("client error during mempool.FlushAppConn", "err", err)
		return err
	}

	// Commit block, get hash back
	res, err := vm.app.Consensus().CommitSync()
	if err != nil {
		vm.log.Error("client error during proxyAppConn.CommitSync", "err", err)
		return err
	}

	// Update the state with the block and responses.
	state.LastBlockHeight = block.Block.Height
	state.LastBlockID = blockID
	state.LastBlockTime = block.Time
	state.LastResultsHash = types.NewResults(abciResponses.DeliverTxs).Hash()
	state.AppHash = res.Data

	// ResponseCommit has no error code - just data
	vm.log.Info(
		"committed state",
		"height", block.Height,
		"num_txs", len(block.Block.Txs),
		"app_hash", fmt.Sprintf("%X", res.Data),
	)

	deliverTxResponses := make([]*abciTypes.ResponseDeliverTx, len(block.Block.Txs))
	for i := range block.Block.Txs {
		deliverTxResponses[i] = &abciTypes.ResponseDeliverTx{Code: abciTypes.CodeTypeOK}
	}

	// Update mempool.
	if err := vm.mempool.Update(
		block.Block.Height,
		block.Block.Txs,
		deliverTxResponses,
		TxPreCheck(state),
		TxPostCheck(state),
	); err != nil {
		return err
	}

	if err := vm.stateStore.Save(state); err != nil {
		return err
	}
	vm.state = state

	vm.blockStore.SaveBlock(block.Block, block.Block.MakePartSet(types.BlockPartSizeBytes), block.Block.LastCommit)
	fireEvents(vm.log, vm.eventBus, block.Block, abciResponses)
	return nil
}
