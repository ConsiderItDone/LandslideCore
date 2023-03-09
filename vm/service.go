package vm

import (
	"context"
	"errors"
	"fmt"
	abci "github.com/tendermint/tendermint/abci/types"
	mempl "github.com/tendermint/tendermint/mempool"
	"github.com/tendermint/tendermint/rpc/core"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
	"github.com/tendermint/tendermint/types"
	"net/http"
	"time"
)

// Service is the API service for this VM
type Service struct {
	vm *VM
}

type HTTP interface {
	BroadcastTxCommit(_ *http.Request, args *BroadcastTxArgs, reply *ctypes.ResultBroadcastTxCommit) error
}

// BroadcastTxArgs are the arguments to function BroadcastTxCommit
type BroadcastTxArgs struct {
	Tx []byte `json:"tx"`
}

// ProposeBlockReply is the reply from function ProposeBlock
type ProposeBlockReply struct{ Success bool }

func (s *Service) BroadcastTxCommit(_ *http.Request, args *BroadcastTxArgs, reply *ctypes.ResultBroadcastTxCommit) error {
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
		reply = &ctypes.ResultBroadcastTxCommit{
			CheckTx:   *checkTxRes,
			DeliverTx: abci.ResponseDeliverTx{},
			Hash:      types.Tx(args.Tx).Hash(),
		}
		return nil
	}

	// Wait for the tx to be included in a block or timeout.
	select {
	case msg := <-deliverTxSub.Out(): // The tx was included in a block.
		deliverTxRes := msg.Data().(types.EventDataTx)
		reply = &ctypes.ResultBroadcastTxCommit{
			CheckTx:   *checkTxRes,
			DeliverTx: deliverTxRes.Result,
			Hash:      types.Tx(args.Tx).Hash(),
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

func (s *Service) BroadcastTxSync(_ *http.Request, args *BroadcastTxArgs, reply *ctypes.ResultBroadcastTx) error {
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
	reply.Hash = types.Tx(args.Tx).Hash()

	return nil
}
