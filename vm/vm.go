package vm

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ava-labs/avalanchego/api/metrics"
	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/database/manager"
	"github.com/ava-labs/avalanchego/database/prefixdb"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/choices"
	"github.com/ava-labs/avalanchego/snow/consensus/snowman"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/snow/engine/snowman/block"
	"github.com/ava-labs/avalanchego/utils/json"
	"github.com/ava-labs/avalanchego/utils/timer/mockable"
	"github.com/ava-labs/avalanchego/version"
	"github.com/ava-labs/avalanchego/vms/components/chain"
	"github.com/gorilla/rpc/v2"
	"github.com/prometheus/client_golang/prometheus"
	dbm "github.com/tendermint/tm-db"

	abciTypes "github.com/consideritdone/landslidecore/abci/types"
	"github.com/consideritdone/landslidecore/config"
	cs "github.com/consideritdone/landslidecore/consensus"
	tmjson "github.com/consideritdone/landslidecore/libs/json"
	"github.com/consideritdone/landslidecore/libs/log"
	mempl "github.com/consideritdone/landslidecore/mempool"
	"github.com/consideritdone/landslidecore/node"
	tmproto "github.com/consideritdone/landslidecore/proto/tendermint/types"
	"github.com/consideritdone/landslidecore/proxy"
	rpccore "github.com/consideritdone/landslidecore/rpc/core"
	rpcserver "github.com/consideritdone/landslidecore/rpc/jsonrpc/server"
	sm "github.com/consideritdone/landslidecore/state"
	"github.com/consideritdone/landslidecore/state/indexer"
	blockidxkv "github.com/consideritdone/landslidecore/state/indexer/block/kv"
	"github.com/consideritdone/landslidecore/state/txindex"
	txidxkv "github.com/consideritdone/landslidecore/state/txindex/kv"
	"github.com/consideritdone/landslidecore/store"
	"github.com/consideritdone/landslidecore/types"
)

var (
	_ block.ChainVM = &VM{}

	Version = &version.Semantic{
		Major: 0,
		Minor: 1,
		Patch: 1,
	}
)

const (
	Name = "landslide"

	decidedCacheSize    = 100
	missingCacheSize    = 50
	unverifiedCacheSize = 50

	// genesisChunkSize is the maximum size, in bytes, of each
	// chunk in the genesis structure for the chunked API
	genesisChunkSize = 16 * 1024 * 1024 // 16
)

var (
	chainStateMetricsPrefix = "chain_state"

	lastAcceptedKey      = []byte("last_accepted_key")
	blockStoreDBPrefix   = []byte("blockstore")
	stateDBPrefix        = []byte("state")
	txIndexerDBPrefix    = []byte("tx_index")
	blockIndexerDBPrefix = []byte("block_events")

	proposerAddress = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
)

var (
	errInvalidBlock = errors.New("invalid block")
	errNoPendingTxs = errors.New("there is no txs to include to block")
)

type VM struct {
	ctx       *snow.Context
	dbManager manager.Manager

	toEngine chan<- common.Message

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
	// cache of chunked genesis data.
	genChunks []string

	// Metrics
	multiGatherer metrics.MultiGatherer

	txIndexer      txindex.TxIndexer
	txIndexerDB    dbm.DB
	blockIndexer   indexer.BlockIndexer
	blockIndexerDB dbm.DB
	indexerService *txindex.IndexerService

	clock mockable.Clock
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

	vm.toEngine = toEngine

	baseDB := dbManager.Current().Database

	vm.blockStoreDB = Database{prefixdb.NewNested(blockStoreDBPrefix, baseDB)}
	vm.blockStore = store.NewBlockStore(vm.blockStoreDB)

	vm.stateDB = Database{prefixdb.NewNested(stateDBPrefix, baseDB)}
	vm.stateStore = sm.NewStore(vm.stateDB)

	if err := vm.initGenesis(genesisBytes); err != nil {
		return err
	}

	if err := vm.initGenesisChunks(); err != nil {
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

	vm.txIndexerDB = Database{prefixdb.NewNested(txIndexerDBPrefix, baseDB)}
	vm.txIndexer = txidxkv.NewTxIndex(vm.txIndexerDB)
	vm.blockIndexerDB = Database{prefixdb.NewNested(blockIndexerDBPrefix, baseDB)}
	vm.blockIndexer = blockidxkv.New(vm.blockIndexerDB)
	vm.indexerService = txindex.NewIndexerService(vm.txIndexer, vm.blockIndexer, eventBus)
	vm.indexerService.SetLogger(vm.tmLogger.With("module", "txindex"))

	if err := vm.indexerService.Start(); err != nil {
		return err
	}

	if err := vm.doHandshake(vm.genesis, vm.tmLogger.With("module", "consensus")); err != nil {
		return err
	}

	state, err = vm.stateStore.Load()
	if err != nil {
		return fmt.Errorf("failed to load tmState: %w ", err)
	}
	vm.tmState = &state

	genesisBlock, err := vm.buildGenesisBlock(genesisBytes)
	if err != nil {
		return fmt.Errorf("failed to build genesis block: %w ", err)
	}

	vm.mempool = vm.createMempool()

	if err := vm.initializeMetrics(); err != nil {
		return err
	}

	if err := vm.initChainState(genesisBlock); err != nil {
		return err
	}

	return nil
}

// builds genesis block if required
func (vm *VM) buildGenesisBlock(genesisData []byte) (*types.Block, error) {
	if vm.tmState.LastBlockHeight != 0 {
		return nil, nil
	}
	txs := types.Txs{types.Tx(genesisData)}
	if len(txs) == 0 {
		return nil, errNoPendingTxs
	}
	height := vm.tmState.LastBlockHeight + 1

	commit := makeCommitMock(height, time.Now())
	genesisBlock, _ := vm.tmState.MakeBlock(height, txs, commit, nil, proposerAddress)
	return genesisBlock, nil
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
		} else {
			return err
		}
	}

	vm.genesis = genesis
	return nil
}

// InitGenesisChunks configures the environment
// and should be called on service startup.
func (vm *VM) initGenesisChunks() error {
	if vm.genesis == nil {
		return fmt.Errorf("empty genesis")
	}

	data, err := tmjson.Marshal(vm.genesis)
	if err != nil {
		return err
	}

	for i := 0; i < len(data); i += genesisChunkSize {
		end := i + genesisChunkSize

		if end > len(data) {
			end = len(data)
		}

		vm.genChunks = append(vm.genChunks, base64.StdEncoding.EncodeToString(data[i:end]))
	}

	return nil
}

func (vm *VM) createMempool() *mempl.CListMempool {
	cfg := config.DefaultMempoolConfig()
	mempool := mempl.NewCListMempool(
		cfg,
		vm.proxyApp.Mempool(),
		vm.tmState.LastBlockHeight,
		vm,
		mempl.WithMetrics(mempl.NopMetrics()), // TODO: use prometheus metrics based on config
		mempl.WithPreCheck(sm.TxPreCheck(*vm.tmState)),
		mempl.WithPostCheck(sm.TxPostCheck(*vm.tmState)),
	)
	mempoolLogger := vm.tmLogger.With("module", "mempool")
	mempool.SetLogger(mempoolLogger)

	return mempool
}

// NotifyBlockReady tells the consensus engine that a new block
// is ready to be created
func (vm *VM) NotifyBlockReady() {
	select {
	case vm.toEngine <- common.PendingTxs:
		vm.tmLogger.Debug("Notify consensys engine")
	default:
		vm.tmLogger.Error("Failed to push PendingTxs notification to the consensus engine.")
	}
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
	vm.multiGatherer = metrics.NewMultiGatherer()

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

func (vm *VM) applyBlock(block *Block) error {
	vm.mempool.Lock()
	defer vm.mempool.Unlock()

	state, err := vm.stateStore.Load()
	if err != nil {
		return err
	}

	if err := validateBlock(state, block.tmBlock); err != nil {
		return err
	}

	abciResponses, err := execBlockOnProxyApp(
		vm.tmLogger,
		vm.proxyApp.Consensus(),
		block.tmBlock, vm.stateStore,
		state.InitialHeight,
	)
	if err != nil {
		return err
	}

	// Save the results before we commit.
	if err := vm.stateStore.SaveABCIResponses(block.tmBlock.Height, abciResponses); err != nil {
		return err
	}

	blockID := types.BlockID{
		Hash:          block.tmBlock.Hash(),
		PartSetHeader: block.tmBlock.MakePartSet(types.BlockPartSizeBytes).Header(),
	}

	// Update the state with the block and responses.
	state, err = updateState(state, blockID, &block.tmBlock.Header, abciResponses)
	if err != nil {
		return err
	}

	// while mempool is Locked, flush to ensure all async requests have completed
	// in the ABCI app before Commit.
	if err := vm.mempool.FlushAppConn(); err != nil {
		vm.tmLogger.Error("client error during mempool.FlushAppConn", "err", err)
		return err
	}

	// Commit block, get hash back
	res, err := vm.proxyApp.Consensus().CommitSync()
	if err != nil {
		vm.tmLogger.Error("client error during proxyAppConn.CommitSync", "err", err)
		return err
	}

	// ResponseCommit has no error code - just data
	vm.tmLogger.Info(
		"committed state",
		"height", block.Height,
		"num_txs", len(block.tmBlock.Txs),
		"app_hash", fmt.Sprintf("%X", res.Data),
	)

	deliverTxResponses := make([]*abciTypes.ResponseDeliverTx, len(block.tmBlock.Txs))
	for i := range block.tmBlock.Txs {
		deliverTxResponses[i] = &abciTypes.ResponseDeliverTx{Code: abciTypes.CodeTypeOK}
	}

	// Update mempool.
	if err := vm.mempool.Update(
		block.tmBlock.Height,
		block.tmBlock.Txs,
		deliverTxResponses,
		TxPreCheck(state),
		TxPostCheck(state),
	); err != nil {
		return err
	}

	vm.tmState.LastBlockHeight = block.tmBlock.Height
	if err := vm.stateStore.Save(state); err != nil {
		return err
	}
	vm.blockStore.SaveBlock(block.tmBlock, block.tmBlock.MakePartSet(types.BlockPartSizeBytes), block.tmBlock.LastCommit)

	fireEvents(vm.tmLogger, vm.eventBus, block.tmBlock, abciResponses)
	return nil
}

// buildBlock builds a block to be wrapped by ChainState
func (vm *VM) buildBlock(_ context.Context) (snowman.Block, error) {
	txs := vm.mempool.ReapMaxBytesMaxGas(-1, -1)
	if len(txs) == 0 {
		return nil, errNoPendingTxs
	}
	height := vm.tmState.LastBlockHeight + 1

	commit := makeCommitMock(height, time.Now())
	block, _ := vm.tmState.MakeBlock(height, txs, commit, nil, proposerAddress)

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
	// first stop the non-reactor services
	if err := vm.eventBus.Stop(); err != nil {
		return fmt.Errorf("Error closing eventBus: %w ", err)
	}
	if err := vm.indexerService.Stop(); err != nil {
		return fmt.Errorf("Error closing indexerService: %w ", err)
	}
	//TODO: investigate wal configuration
	// stop mempool WAL
	//if vm.config.Mempool.WalEnabled() {
	//	n.mempool.CloseWAL()
	//}
	//if n.prometheusSrv != nil {
	//	if err := n.prometheusSrv.Shutdown(context.Background()); err != nil {
	//		// Error from closing listeners, or context timeout:
	//		n.Logger.Error("Prometheus HTTP server Shutdown", "err", err)
	//	}
	//}
	if err := vm.blockStore.Close(); err != nil {
		return fmt.Errorf("Error closing blockStore: %w ", err)
	}
	if err := vm.stateStore.Close(); err != nil {
		return fmt.Errorf("Error closing stateStore: %w ", err)
	}
	return nil
	//timestampVM and deprecated landslide
	//if vm.state == nil {
	//	return nil
	//}
	//
	//return vm.state.Close() // close versionDB

	//coreth
	//if vm.ctx == nil {
	//	return nil
	//}
	//vm.Network.Shutdown()
	//if err := vm.StateSyncClient.Shutdown(); err != nil {
	//	log.Error("error stopping state syncer", "err", err)
	//}
	//close(vm.shutdownChan)
	//vm.eth.Stop()
	//vm.shutdownWg.Wait()
	//return nil
}

func (vm *VM) Version(ctx context.Context) (string, error) {
	return Version.String(), nil
}

func (vm *VM) CreateStaticHandlers(ctx context.Context) (map[string]*common.HTTPHandler, error) {
	//TODO implement me
	return nil, nil
}

func (vm *VM) CreateHandlers(_ context.Context) (map[string]*common.HTTPHandler, error) {
	mux := http.NewServeMux()
	rpcLogger := vm.tmLogger.With("module", "rpc-server")
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

func (vm *VM) ProxyApp() proxy.AppConns {
	return vm.proxyApp
}

func (vm *VM) SetPreference(ctx context.Context, blkID ids.ID) error {
	//TODO implement me
	return nil
}

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
