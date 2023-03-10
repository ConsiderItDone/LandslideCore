package vm

import (
	"context"
	"errors"
	"fmt"
	"time"

	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/libs/bytes"
	mempl "github.com/tendermint/tendermint/mempool"
	"github.com/tendermint/tendermint/proxy"
	"github.com/tendermint/tendermint/rpc/client"
	"github.com/tendermint/tendermint/rpc/core"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
	"github.com/tendermint/tendermint/types"
)

type (
	// Service is the API service for this VM
	Service interface {
		client.ABCIClient
	}

	LocalService struct {
		vm *VM
	}
)

func NewService(vm *VM) Service {
	return &LocalService{vm}
}

func (s *LocalService) ABCIInfo(context.Context) (*ctypes.ResultABCIInfo, error) {
	resInfo, err := s.vm.proxyApp.Query().InfoSync(proxy.RequestInfo)
	if err != nil {
		return nil, err
	}
	return &ctypes.ResultABCIInfo{Response: *resInfo}, nil
}

func (s *LocalService) ABCIQuery(ctx context.Context, path string, data bytes.HexBytes) (*ctypes.ResultABCIQuery, error) {
	return s.ABCIQueryWithOptions(ctx, path, data, client.DefaultABCIQueryOptions)
}

func (s *LocalService) ABCIQueryWithOptions(ctx context.Context, path string, data bytes.HexBytes, opts client.ABCIQueryOptions) (*ctypes.ResultABCIQuery, error) {
	resQuery, err := s.vm.proxyApp.Query().QuerySync(abci.RequestQuery{
		Path:   path,
		Data:   data,
		Height: opts.Height,
		Prove:  opts.Prove,
	})
	if err != nil {
		return nil, err
	}
	return &ctypes.ResultABCIQuery{Response: *resQuery}, nil
}

func (s *LocalService) BroadcastTxCommit(ctx context.Context, tx types.Tx) (*ctypes.ResultBroadcastTxCommit, error) {
	subscriber := ""

	// Subscribe to tx being committed in block.
	subCtx, cancel := context.WithTimeout(ctx, core.SubscribeTimeout)
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

func (s *LocalService) BroadcastTxAsync(ctx context.Context, tx types.Tx) (*ctypes.ResultBroadcastTx, error) {
	err := s.vm.mempool.CheckTx(tx, nil, mempl.TxInfo{})

	if err != nil {
		return nil, err
	}
	return &ctypes.ResultBroadcastTx{Hash: tx.Hash()}, nil
}

func (s *LocalService) BroadcastTxSync(ctx context.Context, tx types.Tx) (*ctypes.ResultBroadcastTx, error) {
	resCh := make(chan *abci.Response, 1)
	err := s.vm.mempool.CheckTx(tx, func(res *abci.Response) {
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
