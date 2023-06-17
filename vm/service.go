package vm

import (
	"context"
	"errors"
	"fmt"
	tmpubsub "github.com/consideritdone/landslidecore/libs/pubsub"
	"github.com/consideritdone/landslidecore/proxy"
	rpctypes "github.com/consideritdone/landslidecore/rpc/jsonrpc/types"
	blockidxnull "github.com/consideritdone/landslidecore/state/indexer/block/null"
	"github.com/consideritdone/landslidecore/state/txindex/null"
	"sort"
	"time"

	abci "github.com/consideritdone/landslidecore/abci/types"
	"github.com/consideritdone/landslidecore/libs/bytes"
	tmbytes "github.com/consideritdone/landslidecore/libs/bytes"
	tmmath "github.com/consideritdone/landslidecore/libs/math"
	tmquery "github.com/consideritdone/landslidecore/libs/pubsub/query"
	mempl "github.com/consideritdone/landslidecore/mempool"
	"github.com/consideritdone/landslidecore/p2p"
	"github.com/consideritdone/landslidecore/rpc/core"
	ctypes "github.com/consideritdone/landslidecore/rpc/core/types"
	"github.com/consideritdone/landslidecore/types"
)

const SubscribeTimeout = 5 * time.Second

type (
	LocalService struct {
		vm *VM
	}

	Service interface {
		ABCIService
		EventsService
		HistoryClient
		NetworkClient
		SignClient
		StatusClient
		MempoolClient
	}

	ABCIService interface {
		// Reading from abci app
		ABCIInfo(ctx *rpctypes.Context) (*ctypes.ResultABCIInfo, error)
		ABCIQuery(ctx *rpctypes.Context, path string, data bytes.HexBytes, height int64, prove bool) (*ctypes.ResultABCIQuery, error)

		// Writing to abci app
		BroadcastTxCommit(*rpctypes.Context, types.Tx) (*ctypes.ResultBroadcastTxCommit, error)
		BroadcastTxAsync(*rpctypes.Context, types.Tx) (*ctypes.ResultBroadcastTx, error)
		BroadcastTxSync(*rpctypes.Context, types.Tx) (*ctypes.ResultBroadcastTx, error)
	}

	EventsService interface {
		Subscribe(ctx *rpctypes.Context, query string) (*ctypes.ResultSubscribe, error)
		Unsubscribe(ctx *rpctypes.Context, query string) (*ctypes.ResultUnsubscribe, error)
		UnsubscribeAll(ctx *rpctypes.Context) (*ctypes.ResultUnsubscribe, error)
	}

	HistoryClient interface {
		Genesis(ctx *rpctypes.Context) (*ctypes.ResultGenesis, error)
		GenesisChunked(*rpctypes.Context, uint) (*ctypes.ResultGenesisChunk, error)
		BlockchainInfo(ctx *rpctypes.Context, minHeight, maxHeight int64) (*ctypes.ResultBlockchainInfo, error)
	}

	MempoolClient interface {
		UnconfirmedTxs(ctx *rpctypes.Context, limit *int) (*ctypes.ResultUnconfirmedTxs, error)
		NumUnconfirmedTxs(ctx *rpctypes.Context) (*ctypes.ResultUnconfirmedTxs, error)
		CheckTx(*rpctypes.Context, types.Tx) (*ctypes.ResultCheckTx, error)
	}

	NetworkClient interface {
		NetInfo(ctx *rpctypes.Context) (*ctypes.ResultNetInfo, error)
		DumpConsensusState(ctx *rpctypes.Context) (*ctypes.ResultDumpConsensusState, error)
		ConsensusState(ctx *rpctypes.Context) (*ctypes.ResultConsensusState, error)
		ConsensusParams(ctx *rpctypes.Context, height *int64) (*ctypes.ResultConsensusParams, error)
		Health(ctx *rpctypes.Context) (*ctypes.ResultHealth, error)
	}

	SignClient interface {
		Block(ctx *rpctypes.Context, height *int64) (*ctypes.ResultBlock, error)
		BlockByHash(ctx *rpctypes.Context, hash []byte) (*ctypes.ResultBlock, error)
		BlockResults(ctx *rpctypes.Context, height *int64) (*ctypes.ResultBlockResults, error)
		Commit(ctx *rpctypes.Context, height *int64) (*ctypes.ResultCommit, error)
		Validators(ctx *rpctypes.Context, height *int64, page, perPage *int) (*ctypes.ResultValidators, error)
		Tx(ctx *rpctypes.Context, hash []byte, prove bool) (*ctypes.ResultTx, error)

		TxSearch(ctx *rpctypes.Context, query string, prove bool,
			page, perPage *int, orderBy string) (*ctypes.ResultTxSearch, error)

		BlockSearch(ctx *rpctypes.Context, query string,
			page, perPage *int, orderBy string) (*ctypes.ResultBlockSearch, error)
	}

	StatusClient interface {
		Status(*rpctypes.Context) (*ctypes.ResultStatus, error)
	}
)

func (s *LocalService) Subscribe(ctx *rpctypes.Context, query string) (*ctypes.ResultSubscribe, error) {
	addr := ctx.RemoteAddr()

	if s.vm.eventBus.NumClients() >= s.vm.rpcConfig.MaxSubscriptionClients {
		return nil, fmt.Errorf("max_subscription_clients %d reached", s.vm.rpcConfig.MaxSubscriptionClients)
	} else if s.vm.eventBus.NumClientSubscriptions(addr) >= s.vm.rpcConfig.MaxSubscriptionsPerClient {
		return nil, fmt.Errorf("max_subscriptions_per_client %d reached", s.vm.rpcConfig.MaxSubscriptionsPerClient)
	}

	s.vm.tmLogger.Info("Subscribe to query", "remote", addr, "query", query)

	q, err := tmquery.New(query)
	if err != nil {
		return nil, fmt.Errorf("failed to parse query: %w", err)
	}

	subCtx, cancel := context.WithTimeout(ctx.Context(), SubscribeTimeout)
	defer cancel()

	sub, err := s.vm.eventBus.Subscribe(subCtx, addr, q, s.vm.rpcConfig.SubscriptionBufferSize)
	if err != nil {
		return nil, err
	}

	closeIfSlow := s.vm.rpcConfig.CloseOnSlowClient

	// Capture the current ID, since it can change in the future.
	subscriptionID := ctx.JSONReq.ID
	go func() {
		for {
			select {
			case msg := <-sub.Out():
				var (
					resultEvent = &ctypes.ResultEvent{Query: query, Data: msg.Data(), Events: msg.Events()}
					resp        = rpctypes.NewRPCSuccessResponse(subscriptionID, resultEvent)
				)
				writeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err = ctx.WSConn.WriteRPCResponse(writeCtx, resp); err != nil {
					s.vm.tmLogger.Info("Can't write response (slow client)",
						"to", addr, "subscriptionID", subscriptionID, "err", err)

					if closeIfSlow {
						var (
							err  = errors.New("subscription was cancelled (reason: slow client)")
							resp = rpctypes.RPCServerError(subscriptionID, err)
						)
						if !ctx.WSConn.TryWriteRPCResponse(resp) {
							s.vm.tmLogger.Info("Can't write response (slow client)",
								"to", addr, "subscriptionID", subscriptionID, "err", err)
						}
						return
					}
				}
			case <-sub.Cancelled():
				if sub.Err() != tmpubsub.ErrUnsubscribed {
					var reason string
					if sub.Err() == nil {
						reason = "Tendermint exited"
					} else {
						reason = sub.Err().Error()
					}
					resp := rpctypes.RPCServerError(subscriptionID, err)
					if !ctx.WSConn.TryWriteRPCResponse(resp) {
						s.vm.tmLogger.Info("Can't write response (slow client)",
							"to", addr, "subscriptionID", subscriptionID, "err",
							fmt.Errorf("subscription was cancelled (reason: %s)", reason))
					}
				}
				return
			}
		}
	}()

	return &ctypes.ResultSubscribe{}, nil
}

func (s *LocalService) Unsubscribe(ctx *rpctypes.Context, query string) (*ctypes.ResultUnsubscribe, error) {
	addr := ctx.RemoteAddr()
	s.vm.tmLogger.Info("Unsubscribe from query", "remote", addr, "query", query)
	q, err := tmquery.New(query)
	if err != nil {
		return nil, fmt.Errorf("failed to parse query: %w", err)
	}
	err = s.vm.eventBus.Unsubscribe(context.Background(), addr, q)
	if err != nil {
		return nil, err
	}
	return &ctypes.ResultUnsubscribe{}, nil
}

func (s *LocalService) UnsubscribeAll(ctx *rpctypes.Context) (*ctypes.ResultUnsubscribe, error) {
	addr := ctx.RemoteAddr()
	s.vm.tmLogger.Info("Unsubscribe from all", "remote", addr)
	err := s.vm.eventBus.UnsubscribeAll(context.Background(), addr)
	if err != nil {
		return nil, err
	}
	return &ctypes.ResultUnsubscribe{}, nil
}

func NewService(vm *VM) Service {
	return &LocalService{vm}
}

func (s *LocalService) ABCIInfo(ctx *rpctypes.Context) (*ctypes.ResultABCIInfo, error) {
	resInfo, err := s.vm.proxyApp.Query().InfoSync(proxy.RequestInfo)
	if err != nil || resInfo == nil {
		return nil, err
	}
	return &ctypes.ResultABCIInfo{Response: *resInfo}, nil
}

// TODO: attention! Different signatures in RPC interfaces
func (s *LocalService) ABCIQuery(
	ctx *rpctypes.Context,
	path string,
	data bytes.HexBytes,
	height int64,
	prove bool,
) (*ctypes.ResultABCIQuery, error) {
	resQuery, err := s.vm.proxyApp.Query().QuerySync(abci.RequestQuery{
		Path:   path,
		Data:   data,
		Height: height,
		Prove:  prove,
	})
	if err != nil || resQuery == nil {
		return nil, err
	}

	return &ctypes.ResultABCIQuery{Response: *resQuery}, nil
}

func (s *LocalService) BroadcastTxCommit(ctx *rpctypes.Context, tx types.Tx) (*ctypes.ResultBroadcastTxCommit, error) {
	subscriber := ""

	// Subscribe to tx being committed in block.
	subCtx, cancel := context.WithTimeout(ctx.Context(), core.SubscribeTimeout)
	defer cancel()

	q := types.EventQueryTxFor(tx)
	deliverTxSub, err := s.vm.eventBus.Subscribe(subCtx, subscriber, q)
	if err != nil {
		err = fmt.Errorf("failed to subscribe to tx: %w", err)
		s.vm.tmLogger.Error("Error on broadcast_tx_commit", "err", err)
		return nil, err
	}

	defer func() {
		if err := s.vm.eventBus.Unsubscribe(context.Background(), subscriber, q); err != nil {
			s.vm.tmLogger.Error("Error unsubscribing from eventBus", "err", err)
		}
	}()

	// Broadcast tx and wait for CheckTx result
	checkTxResCh := make(chan *abci.Response, 1)
	err = s.vm.mempool.CheckTx(tx, func(res *abci.Response) {
		checkTxResCh <- res
	}, mempl.TxInfo{})
	if err != nil {
		s.vm.tmLogger.Error("Error on broadcastTxCommit", "err", err)
		return nil, fmt.Errorf("error on broadcastTxCommit: %v", err)
	}
	checkTxResMsg := <-checkTxResCh
	checkTxRes := checkTxResMsg.GetCheckTx()
	if checkTxRes.Code != abci.CodeTypeOK {
		return &ctypes.ResultBroadcastTxCommit{
			CheckTx:   *checkTxRes,
			DeliverTx: abci.ResponseDeliverTx{},
			Hash:      tx.Hash(),
		}, nil
	}

	// Wait for the tx to be included in a block or timeout.
	select {
	case msg := <-deliverTxSub.Out(): // The tx was included in a block.
		deliverTxRes := msg.Data().(types.EventDataTx)
		return &ctypes.ResultBroadcastTxCommit{
			CheckTx:   *checkTxRes,
			DeliverTx: deliverTxRes.Result,
			Hash:      tx.Hash(),
			Height:    deliverTxRes.Height,
		}, nil
	case <-deliverTxSub.Cancelled():
		var reason string
		if deliverTxSub.Err() == nil {
			reason = "Tendermint exited"
		} else {
			reason = deliverTxSub.Err().Error()
		}
		err = fmt.Errorf("deliverTxSub was cancelled (reason: %s)", reason)
		s.vm.tmLogger.Error("Error on broadcastTxCommit", "err", err)
		return &ctypes.ResultBroadcastTxCommit{
			CheckTx:   *checkTxRes,
			DeliverTx: abci.ResponseDeliverTx{},
			Hash:      tx.Hash(),
		}, err
	// TODO: use config for timeout
	case <-time.After(10 * time.Second):
		err = errors.New("timed out waiting for tx to be included in a block")
		s.vm.tmLogger.Error("Error on broadcastTxCommit", "err", err)
		return &ctypes.ResultBroadcastTxCommit{
			CheckTx:   *checkTxRes,
			DeliverTx: abci.ResponseDeliverTx{},
			Hash:      tx.Hash(),
		}, err
	}
}

func (s *LocalService) BroadcastTxAsync(ctx *rpctypes.Context, tx types.Tx) (*ctypes.ResultBroadcastTx, error) {
	err := s.vm.mempool.CheckTx(tx, nil, mempl.TxInfo{})
	if err != nil {
		return nil, err
	}
	return &ctypes.ResultBroadcastTx{Hash: tx.Hash()}, nil
}

func (s *LocalService) BroadcastTxSync(ctx *rpctypes.Context, tx types.Tx) (*ctypes.ResultBroadcastTx, error) {
	resCh := make(chan *abci.Response, 1)
	err := s.vm.mempool.CheckTx(tx, func(res *abci.Response) {
		s.vm.tmLogger.With("module", "service").Debug("handled response from checkTx")
		resCh <- res
	}, mempl.TxInfo{})
	if err != nil {
		return nil, err
	}
	res := <-resCh
	r := res.GetCheckTx()
	return &ctypes.ResultBroadcastTx{
		Code:      r.Code,
		Data:      r.Data,
		Log:       r.Log,
		Codespace: r.Codespace,
		Hash:      tx.Hash(),
	}, nil
}

func (s *LocalService) Block(ctx *rpctypes.Context, heightPtr *int64) (*ctypes.ResultBlock, error) {
	height, err := getHeight(s.vm.blockStore, heightPtr)
	if err != nil {
		return nil, err
	}

	block := s.vm.blockStore.LoadBlock(height)
	blockMeta := s.vm.blockStore.LoadBlockMeta(height)
	if blockMeta == nil {
		return &ctypes.ResultBlock{BlockID: types.BlockID{}, Block: block}, nil
	}
	return &ctypes.ResultBlock{BlockID: blockMeta.BlockID, Block: block}, nil
}

func (s *LocalService) BlockByHash(ctx *rpctypes.Context, hash []byte) (*ctypes.ResultBlock, error) {
	block := s.vm.blockStore.LoadBlockByHash(hash)
	if block == nil {
		return &ctypes.ResultBlock{BlockID: types.BlockID{}, Block: nil}, nil
	}
	// If block is not nil, then blockMeta can't be nil.
	blockMeta := s.vm.blockStore.LoadBlockMeta(block.Height)
	return &ctypes.ResultBlock{BlockID: blockMeta.BlockID, Block: block}, nil
}

func (s *LocalService) BlockResults(ctx *rpctypes.Context, heightPtr *int64) (*ctypes.ResultBlockResults, error) {
	height, err := getHeight(s.vm.blockStore, heightPtr)
	if err != nil {
		return nil, err
	}

	results, err := s.vm.stateStore.LoadABCIResponses(height)
	if err != nil {
		return nil, err
	}

	return &ctypes.ResultBlockResults{
		Height:                height,
		TxsResults:            results.DeliverTxs,
		BeginBlockEvents:      results.BeginBlock.Events,
		EndBlockEvents:        results.EndBlock.Events,
		ValidatorUpdates:      results.EndBlock.ValidatorUpdates,
		ConsensusParamUpdates: results.EndBlock.ConsensusParamUpdates,
	}, nil
}

func (s *LocalService) Commit(ctx *rpctypes.Context, heightPtr *int64) (*ctypes.ResultCommit, error) {
	height, err := getHeight(s.vm.blockStore, heightPtr)
	if err != nil {
		return nil, err
	}

	blockMeta := s.vm.blockStore.LoadBlockMeta(height)
	if blockMeta == nil {
		return nil, nil
	}
	header := blockMeta.Header

	// Return the canonical commit (comes from the block at height+1)
	commit := s.vm.blockStore.LoadBlockCommit(height)
	return ctypes.NewResultCommit(&header, commit, true), nil
}

func (s *LocalService) Validators(ctx *rpctypes.Context, heightPtr *int64, pagePtr, perPagePtr *int) (*ctypes.ResultValidators, error) {
	height, err := getHeight(s.vm.blockStore, heightPtr)
	if err != nil {
		return nil, err
	}

	validators, err := s.vm.stateStore.LoadValidators(height)
	if err != nil {
		return nil, err
	}

	totalCount := len(validators.Validators)
	perPage := validatePerPage(perPagePtr)
	page, err := validatePage(pagePtr, perPage, totalCount)
	if err != nil {
		return nil, err
	}

	skipCount := validateSkipCount(page, perPage)

	v := validators.Validators[skipCount : skipCount+tmmath.MinInt(perPage, totalCount-skipCount)]

	return &ctypes.ResultValidators{
		BlockHeight: height,
		Validators:  v,
		Count:       len(v),
		Total:       totalCount}, nil
}

func (s *LocalService) Tx(ctx *rpctypes.Context, hash []byte, prove bool) (*ctypes.ResultTx, error) {
	if _, ok := s.vm.txIndexer.(*null.TxIndex); ok {
		return nil, fmt.Errorf("transaction indexing is disabled")
	}

	r, err := s.vm.txIndexer.Get(hash)
	if err != nil {
		return nil, err
	}

	if r == nil {
		return nil, fmt.Errorf("tx (%X) not found", hash)
	}

	height := r.Height
	index := r.Index

	var proof types.TxProof
	if prove {
		block := s.vm.blockStore.LoadBlock(height)
		proof = block.Data.Txs.Proof(int(index)) // XXX: overflow on 32-bit machines
	}

	return &ctypes.ResultTx{
		Hash:     hash,
		Height:   height,
		Index:    index,
		TxResult: r.Result,
		Tx:       r.Tx,
		Proof:    proof,
	}, nil
}

func (s *LocalService) TxSearch(ctx *rpctypes.Context, query string, prove bool, pagePtr, perPagePtr *int,
	orderBy string) (*ctypes.ResultTxSearch, error) {
	// if index is disabled, return error
	if _, ok := s.vm.txIndexer.(*null.TxIndex); ok {
		return nil, errors.New("transaction indexing is disabled")
	}
	q, err := tmquery.New(query)
	if err != nil {
		return nil, err
	}

	results, err := s.vm.txIndexer.Search(ctx.Context(), q)
	if err != nil {
		return nil, err
	}

	// sort results (must be done before pagination)
	switch orderBy {
	case "desc":
		sort.Slice(results, func(i, j int) bool {
			if results[i].Height == results[j].Height {
				return results[i].Index > results[j].Index
			}
			return results[i].Height > results[j].Height
		})
	case "asc", "":
		sort.Slice(results, func(i, j int) bool {
			if results[i].Height == results[j].Height {
				return results[i].Index < results[j].Index
			}
			return results[i].Height < results[j].Height
		})
	default:
		return nil, errors.New("expected order_by to be either `asc` or `desc` or empty")
	}

	// paginate results
	totalCount := len(results)
	perPage := validatePerPage(perPagePtr)

	page, err := validatePage(pagePtr, perPage, totalCount)
	if err != nil {
		return nil, err
	}

	skipCount := validateSkipCount(page, perPage)
	pageSize := tmmath.MinInt(perPage, totalCount-skipCount)

	apiResults := make([]*ctypes.ResultTx, 0, pageSize)
	for i := skipCount; i < skipCount+pageSize; i++ {
		r := results[i]

		var proof types.TxProof
		if prove {
			block := s.vm.blockStore.LoadBlock(r.Height)
			proof = block.Data.Txs.Proof(int(r.Index)) // XXX: overflow on 32-bit machines
		}

		apiResults = append(apiResults, &ctypes.ResultTx{
			Hash:     types.Tx(r.Tx).Hash(),
			Height:   r.Height,
			Index:    r.Index,
			TxResult: r.Result,
			Tx:       r.Tx,
			Proof:    proof,
		})
	}

	return &ctypes.ResultTxSearch{Txs: apiResults, TotalCount: totalCount}, nil
}

// BlockSearch searches for a paginated set of blocks matching BeginBlock and
// EndBlock event search criteria.
func (s *LocalService) BlockSearch(
	ctx *rpctypes.Context,
	query string,
	pagePtr, perPagePtr *int,
	orderBy string,
) (*ctypes.ResultBlockSearch, error) {

	// skip if block indexing is disabled
	if _, ok := s.vm.blockIndexer.(*blockidxnull.BlockerIndexer); ok {
		return nil, errors.New("block indexing is disabled")
	}

	q, err := tmquery.New(query)
	if err != nil {
		return nil, err
	}

	results, err := s.vm.blockIndexer.Search(ctx.Context(), q)
	if err != nil {
		return nil, err
	}

	// sort results (must be done before pagination)
	switch orderBy {
	case "desc", "":
		sort.Slice(results, func(i, j int) bool { return results[i] > results[j] })

	case "asc":
		sort.Slice(results, func(i, j int) bool { return results[i] < results[j] })

	default:
		return nil, errors.New("expected order_by to be either `asc` or `desc` or empty")
	}

	// paginate results
	totalCount := len(results)
	perPage := validatePerPage(perPagePtr)

	page, err := validatePage(pagePtr, perPage, totalCount)
	if err != nil {
		return nil, err
	}

	skipCount := validateSkipCount(page, perPage)
	pageSize := tmmath.MinInt(perPage, totalCount-skipCount)

	apiResults := make([]*ctypes.ResultBlock, 0, pageSize)
	for i := skipCount; i < skipCount+pageSize; i++ {
		block := s.vm.blockStore.LoadBlock(results[i])
		if block != nil {
			blockMeta := s.vm.blockStore.LoadBlockMeta(block.Height)
			if blockMeta != nil {
				apiResults = append(apiResults, &ctypes.ResultBlock{
					Block:   block,
					BlockID: blockMeta.BlockID,
				})
			}
		}
	}

	return &ctypes.ResultBlockSearch{Blocks: apiResults, TotalCount: totalCount}, nil
}

func (s *LocalService) BlockchainInfo(ctx *rpctypes.Context, minHeight, maxHeight int64) (*ctypes.ResultBlockchainInfo, error) {
	// maximum 20 block metas
	const limit int64 = 20
	var err error
	minHeight, maxHeight, err = filterMinMax(
		s.vm.blockStore.Base(),
		s.vm.blockStore.Height(),
		minHeight,
		maxHeight,
		limit)
	if err != nil {
		return nil, err
	}
	s.vm.tmLogger.Debug("BlockchainInfoHandler", "maxHeight", maxHeight, "minHeight", minHeight)

	var blockMetas []*types.BlockMeta
	for height := maxHeight; height >= minHeight; height-- {
		blockMeta := s.vm.blockStore.LoadBlockMeta(height)
		blockMetas = append(blockMetas, blockMeta)
	}

	return &ctypes.ResultBlockchainInfo{
		LastHeight: s.vm.blockStore.Height(),
		BlockMetas: blockMetas}, nil
}

func (s *LocalService) Genesis(ctx *rpctypes.Context) (*ctypes.ResultGenesis, error) {
	if len(s.vm.genChunks) > 1 {
		return nil, errors.New("genesis response is large, please use the genesis_chunked API instead")
	}

	return &ctypes.ResultGenesis{Genesis: s.vm.genesis}, nil
}

func (s *LocalService) GenesisChunked(ctx *rpctypes.Context, chunk uint) (*ctypes.ResultGenesisChunk, error) {
	if s.vm.genChunks == nil {
		return nil, fmt.Errorf("service configuration error, genesis chunks are not initialized")
	}

	if len(s.vm.genChunks) == 0 {
		return nil, fmt.Errorf("service configuration error, there are no chunks")
	}

	id := int(chunk)

	if id > len(s.vm.genChunks)-1 {
		return nil, fmt.Errorf("there are %d chunks, %d is invalid", len(s.vm.genChunks)-1, id)
	}

	return &ctypes.ResultGenesisChunk{
		TotalChunks: len(s.vm.genChunks),
		ChunkNumber: id,
		Data:        s.vm.genChunks[id],
	}, nil
}

func (s *LocalService) Status(ctx *rpctypes.Context) (*ctypes.ResultStatus, error) {
	var (
		earliestBlockHeight   int64
		earliestBlockHash     tmbytes.HexBytes
		earliestAppHash       tmbytes.HexBytes
		earliestBlockTimeNano int64
	)

	if earliestBlockMeta := s.vm.blockStore.LoadBaseMeta(); earliestBlockMeta != nil {
		earliestBlockHeight = earliestBlockMeta.Header.Height
		earliestAppHash = earliestBlockMeta.Header.AppHash
		earliestBlockHash = earliestBlockMeta.BlockID.Hash
		earliestBlockTimeNano = earliestBlockMeta.Header.Time.UnixNano()
	}

	var (
		latestBlockHash     tmbytes.HexBytes
		latestAppHash       tmbytes.HexBytes
		latestBlockTimeNano int64

		latestHeight = s.vm.blockStore.Height()
	)

	if latestHeight != 0 {
		if latestBlockMeta := s.vm.blockStore.LoadBlockMeta(latestHeight); latestBlockMeta != nil {
			latestBlockHash = latestBlockMeta.BlockID.Hash
			latestAppHash = latestBlockMeta.Header.AppHash
			latestBlockTimeNano = latestBlockMeta.Header.Time.UnixNano()
		}
	}

	result := &ctypes.ResultStatus{
		NodeInfo: p2p.DefaultNodeInfo{
			DefaultNodeID: p2p.ID(s.vm.ctx.NodeID.String()),
			Network:       fmt.Sprintf("%d", s.vm.ctx.NetworkID),
		},
		SyncInfo: ctypes.SyncInfo{
			LatestBlockHash:     latestBlockHash,
			LatestAppHash:       latestAppHash,
			LatestBlockHeight:   latestHeight,
			LatestBlockTime:     time.Unix(0, latestBlockTimeNano),
			EarliestBlockHash:   earliestBlockHash,
			EarliestAppHash:     earliestAppHash,
			EarliestBlockHeight: earliestBlockHeight,
			EarliestBlockTime:   time.Unix(0, earliestBlockTimeNano),
			CatchingUp:          false,
		},
		ValidatorInfo: ctypes.ValidatorInfo{
			Address:     proposerPubKey.Address(),
			PubKey:      proposerPubKey,
			VotingPower: 0,
		},
	}

	return result, nil
}

// ToDo: no peers, no network from tendermint side
func (s *LocalService) NetInfo(ctx *rpctypes.Context) (*ctypes.ResultNetInfo, error) {
	return &ctypes.ResultNetInfo{}, nil
}

// ToDo: we doesn't have consensusState
func (s *LocalService) DumpConsensusState(ctx *rpctypes.Context) (*ctypes.ResultDumpConsensusState, error) {
	return &ctypes.ResultDumpConsensusState{}, nil
}

// ToDo: we doesn't have consensusState
func (s *LocalService) ConsensusState(ctx *rpctypes.Context) (*ctypes.ResultConsensusState, error) {
	return &ctypes.ResultConsensusState{}, nil
}

func (s *LocalService) ConsensusParams(ctx *rpctypes.Context, heightPtr *int64) (*ctypes.ResultConsensusParams, error) {
	return &ctypes.ResultConsensusParams{
		BlockHeight:     s.vm.blockStore.Height(),
		ConsensusParams: *s.vm.genesis.ConsensusParams,
	}, nil
}

func (s *LocalService) Health(ctx *rpctypes.Context) (*ctypes.ResultHealth, error) {
	return &ctypes.ResultHealth{}, nil
}

func (s *LocalService) UnconfirmedTxs(ctx *rpctypes.Context, limitPtr *int) (*ctypes.ResultUnconfirmedTxs, error) {
	limit := validatePerPage(limitPtr)
	txs := s.vm.mempool.ReapMaxTxs(limit)
	return &ctypes.ResultUnconfirmedTxs{
		Count: len(txs),
		Total: s.vm.mempool.Size(),
		Txs:   txs,
	}, nil
}

func (s *LocalService) NumUnconfirmedTxs(ctx *rpctypes.Context) (*ctypes.ResultUnconfirmedTxs, error) {
	return &ctypes.ResultUnconfirmedTxs{
		Count:      s.vm.mempool.Size(),
		Total:      s.vm.mempool.Size(),
		TotalBytes: s.vm.mempool.TxsBytes()}, nil
}

func (s *LocalService) CheckTx(ctx *rpctypes.Context, tx types.Tx) (*ctypes.ResultCheckTx, error) {
	res, err := s.vm.proxyApp.Mempool().CheckTxSync(abci.RequestCheckTx{Tx: tx})
	if err != nil {
		return nil, err
	}
	return &ctypes.ResultCheckTx{ResponseCheckTx: *res}, nil
}
