package vm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/libs/bytes"
	tmbytes "github.com/tendermint/tendermint/libs/bytes"
	mempl "github.com/tendermint/tendermint/mempool"
	"github.com/tendermint/tendermint/proxy"
	"github.com/tendermint/tendermint/rpc/core"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
	"github.com/tendermint/tendermint/types"
)

type (
	// BroadcastTxArgs is the arguments to functions BroadcastTxCommit, BroadcastTxAsync, BroadcastTxSync
	BroadcastTxArgs struct {
		Tx types.Tx `json:"tx"`
	}

	BlockHeightArgs struct {
		Height *int64 `json:"height"`
	}

	BlockHashArgs struct {
		Hash []byte `json:"hash"`
	}

	ABCIInfoArgs struct {
	}

	ABCIQueryArgs struct {
		Path string         `json:"path"`
		Data bytes.HexBytes `json:"data"`
	}

	ABCIQueryOptions struct {
		Height int64 `json:"height"`
		Prove  bool  `json:"prove"`
	}

	ABCIQueryWithOptionsArgs struct {
		Path string           `json:"path"`
		Data bytes.HexBytes   `json:"data"`
		Opts ABCIQueryOptions `json:"opts"`
	}

	StatusArgs struct{}

	// Service is the API service for this VM
	Service interface {
		// Reading from abci app
		ABCIInfo(_ *http.Request, args *ABCIInfoArgs, reply *ctypes.ResultABCIInfo) error
		ABCIQuery(_ *http.Request, args *ABCIQueryArgs, reply *ctypes.ResultABCIQuery) error
		ABCIQueryWithOptions(_ *http.Request, args *ABCIQueryWithOptionsArgs, reply *ctypes.ResultABCIQuery) error

		// Writing to abci app
		BroadcastTxCommit(_ *http.Request, args *BroadcastTxArgs, reply *ctypes.ResultBroadcastTxCommit) error
		BroadcastTxAsync(_ *http.Request, args *BroadcastTxArgs, reply *ctypes.ResultBroadcastTx) error
		BroadcastTxSync(_ *http.Request, args *BroadcastTxArgs, reply *ctypes.ResultBroadcastTx) error

		Block(_ *http.Request, args *BlockHeightArgs, reply *ctypes.ResultBlock) error
		BlockByHash(_ *http.Request, args *BlockHashArgs, reply *ctypes.ResultBlock) error
		BlockResults(_ *http.Request, args *BlockHeightArgs, reply *ctypes.ResultBlockResults) error

		Status(_ *http.Request, args *StatusArgs, reply *ctypes.ResultStatus) error
	}

	LocalService struct {
		vm *VM
	}
)

var (
	DefaultABCIQueryOptions = ABCIQueryOptions{Height: 0, Prove: false}
)

func NewService(vm *VM) Service {
	return &LocalService{vm}
}

func (s *LocalService) ABCIInfo(_ *http.Request, args *ABCIInfoArgs, reply *ctypes.ResultABCIInfo) error {
	resInfo, err := s.vm.proxyApp.Query().InfoSync(proxy.RequestInfo)
	if err != nil {
		return err
	}
	reply.Response = *resInfo
	return nil
}

func (s *LocalService) ABCIQuery(req *http.Request, args *ABCIQueryArgs, reply *ctypes.ResultABCIQuery) error {
	return s.ABCIQueryWithOptions(req, &ABCIQueryWithOptionsArgs{args.Path, args.Data, DefaultABCIQueryOptions}, reply)
}

func (s *LocalService) ABCIQueryWithOptions(
	_ *http.Request,
	args *ABCIQueryWithOptionsArgs,
	reply *ctypes.ResultABCIQuery,
) error {
	resQuery, err := s.vm.proxyApp.Query().QuerySync(abci.RequestQuery{
		Path:   args.Path,
		Data:   args.Data,
		Height: args.Opts.Height,
		Prove:  args.Opts.Prove,
	})
	if err != nil {
		return err
	}
	reply.Response = *resQuery
	return nil
}

func (s *LocalService) BroadcastTxCommit(
	_ *http.Request,
	args *BroadcastTxArgs,
	reply *ctypes.ResultBroadcastTxCommit,
) error {
	subscriber := ""

	// Subscribe to tx being committed in block.
	subCtx, cancel := context.WithTimeout(context.Background(), core.SubscribeTimeout)
	defer cancel()

	q := types.EventQueryTxFor(args.Tx)
	deliverTxSub, err := s.vm.eventBus.Subscribe(subCtx, subscriber, q)
	if err != nil {
		err = fmt.Errorf("failed to subscribe to tx: %w", err)
		s.vm.tmLogger.Error("Error on broadcast_tx_commit", "err", err)
		return err
	}

	defer func() {
		if err := s.vm.eventBus.Unsubscribe(context.Background(), subscriber, q); err != nil {
			s.vm.tmLogger.Error("Error unsubscribing from eventBus", "err", err)
		}
	}()

	// Broadcast tx and wait for CheckTx result
	checkTxResCh := make(chan *abci.Response, 1)
	err = s.vm.mempool.CheckTx(args.Tx, func(res *abci.Response) {
		checkTxResCh <- res
	}, mempl.TxInfo{})
	if err != nil {
		s.vm.tmLogger.Error("Error on broadcastTxCommit", "err", err)
		return fmt.Errorf("error on broadcastTxCommit: %v", err)
	}
	checkTxResMsg := <-checkTxResCh
	checkTxRes := checkTxResMsg.GetCheckTx()
	if checkTxRes.Code != abci.CodeTypeOK {
		*reply = ctypes.ResultBroadcastTxCommit{
			CheckTx:   *checkTxRes,
			DeliverTx: abci.ResponseDeliverTx{},
			Hash:      args.Tx.Hash(),
		}
		return nil
	}

	// Wait for the tx to be included in a block or timeout.
	select {
	case msg := <-deliverTxSub.Out(): // The tx was included in a block.
		deliverTxRes := msg.Data().(types.EventDataTx)
		*reply = ctypes.ResultBroadcastTxCommit{
			CheckTx:   *checkTxRes,
			DeliverTx: deliverTxRes.Result,
			Hash:      args.Tx.Hash(),
			Height:    deliverTxRes.Height,
		}
		return nil
	case <-deliverTxSub.Cancelled():
		var reason string
		if deliverTxSub.Err() == nil {
			reason = "Tendermint exited"
		} else {
			reason = deliverTxSub.Err().Error()
		}
		err = fmt.Errorf("deliverTxSub was cancelled (reason: %s)", reason)
		s.vm.tmLogger.Error("Error on broadcastTxCommit", "err", err)
		return err
	// TODO: use config for timeout
	case <-time.After(10 * time.Second):
		err = errors.New("timed out waiting for tx to be included in a block")
		s.vm.tmLogger.Error("Error on broadcastTxCommit", "err", err)
		return err
	}
}

func (s *LocalService) BroadcastTxAsync(
	_ *http.Request,
	args *BroadcastTxArgs,
	reply *ctypes.ResultBroadcastTx,
) error {
	err := s.vm.mempool.CheckTx(args.Tx, nil, mempl.TxInfo{})
	if err != nil {
		return err
	}
	reply.Hash = args.Tx.Hash()
	return nil
}

func (s *LocalService) BroadcastTxSync(_ *http.Request, args *BroadcastTxArgs, reply *ctypes.ResultBroadcastTx) error {
	resCh := make(chan *abci.Response, 1)
	err := s.vm.mempool.CheckTx(args.Tx, func(res *abci.Response) {
		resCh <- res
	}, mempl.TxInfo{})
	if err != nil {
		return err
	}
	res := <-resCh
	r := res.GetCheckTx()

	reply.Code = r.Code
	reply.Data = r.Data
	reply.Log = r.Log
	reply.Codespace = r.Codespace
	reply.Hash = args.Tx.Hash()

	return nil
}

func (s *LocalService) Block(_ *http.Request, args *BlockHeightArgs, reply *ctypes.ResultBlock) error {
	height, err := getHeight(s.vm.blockStore, args.Height)
	if err != nil {
		return err
	}
	block := s.vm.blockStore.LoadBlock(height)
	blockMeta := s.vm.blockStore.LoadBlockMeta(height)

	if blockMeta != nil {
		reply.BlockID = blockMeta.BlockID
	}
	reply.Block = block
	return nil
}

func (s *LocalService) BlockByHash(_ *http.Request, args *BlockHashArgs, reply *ctypes.ResultBlock) error {
	block := s.vm.blockStore.LoadBlockByHash(args.Hash)
	if block == nil {
		reply.BlockID = types.BlockID{}
		reply.Block = nil
		return nil
	}
	blockMeta := s.vm.blockStore.LoadBlockMeta(block.Height)
	reply.BlockID = blockMeta.BlockID
	reply.Block = block
	return nil
}

func (s *LocalService) BlockResults(_ *http.Request, args *BlockHeightArgs, reply *ctypes.ResultBlockResults) error {
	height, err := getHeight(s.vm.blockStore, args.Height)
	if err != nil {
		return err
	}

	results, err := s.vm.stateStore.LoadABCIResponses(height)
	if err != nil {
		return err
	}

	reply.Height = height
	reply.TxsResults = results.DeliverTxs
	reply.BeginBlockEvents = results.BeginBlock.Events
	reply.EndBlockEvents = results.EndBlock.Events
	reply.ValidatorUpdates = results.EndBlock.ValidatorUpdates
	reply.ConsensusParamUpdates = results.EndBlock.ConsensusParamUpdates
	return nil
}

func (s *LocalService) Status(_ *http.Request, args *StatusArgs, reply *ctypes.ResultStatus) error {
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

	// Return the very last voting power, not the voting power of this validator
	// during the last block.
	var votingPower int64
	// ToDo: implement me
	// if val := validatorAtHeight(latestUncommittedHeight()); val != nil {
	//	votingPower = val.VotingPower
	// }

	// ToDo: implement me
	// reply.NodeInfo = env.P2PTransport.NodeInfo().(p2p.DefaultNodeInfo)
	reply.SyncInfo = ctypes.SyncInfo{
		LatestBlockHash:     latestBlockHash,
		LatestAppHash:       latestAppHash,
		LatestBlockHeight:   latestHeight,
		LatestBlockTime:     time.Unix(0, latestBlockTimeNano),
		EarliestBlockHash:   earliestBlockHash,
		EarliestAppHash:     earliestAppHash,
		EarliestBlockHeight: earliestBlockHeight,
		EarliestBlockTime:   time.Unix(0, earliestBlockTimeNano),
		// ToDo: implement me
		// CatchingUp:          env.ConsensusReactor.WaitSync(),
	}
	reply.ValidatorInfo = ctypes.ValidatorInfo{
		// ToDo: implement me
		// Address:     env.PubKey.Address(),
		// PubKey:      env.PubKey,
		VotingPower: votingPower,
	}
	return nil
}
