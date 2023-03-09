package vm

import (
	"context"
	"fmt"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow/choices"
	"github.com/ava-labs/avalanchego/snow/consensus/snowman"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/types"
	"time"
)

var (
	_ snowman.Block = &Block{}
)

// Block implements the snowman.Block interface
type Block struct {
	id      ids.ID
	tmBlock *types.Block
	vm      *VM
	status  choices.Status
}

// newBlock returns a new Block wrapping the Tendermint Block type and implementing the snowman.Block interface
func (vm *VM) newBlock(tmBlock *types.Block) (*Block, error) {
	var id ids.ID
	copy(id[:], tmBlock.Hash())

	return &Block{
		id:      id,
		tmBlock: tmBlock,
		vm:      vm,
	}, nil
}

func (b *Block) ID() ids.ID {
	return b.id
}

func (b *Block) Accept(ctx context.Context) error {
	b.SetStatus(choices.Accepted)

	vm := b.vm

	// TODO: implement blockExecutor.ApplyBlock

	vm.mempool.Lock()
	defer vm.mempool.Unlock()

	// while mempool is Locked, flush to ensure all async requests have completed
	// in the ABCI app before Commit.
	err := vm.mempool.FlushAppConn()
	if err != nil {
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
		"height", b.Height,
		"num_txs", len(b.tmBlock.Txs),
		"app_hash", fmt.Sprintf("%X", res.Data),
	)

	deliverTxResponses := make([]*abci.ResponseDeliverTx, len(b.tmBlock.Txs))
	for i := range b.tmBlock.Txs {
		deliverTxResponses[i] = &abci.ResponseDeliverTx{Code: abci.CodeTypeOK}
	}

	// Update mempool.
	err = vm.mempool.Update(
		b.tmBlock.Height,
		b.tmBlock.Txs,
		deliverTxResponses,
		nil,
		nil,
	)

	return nil
}

func (b *Block) Reject(ctx context.Context) error {
	b.SetStatus(choices.Rejected)

	return nil
}

func (b *Block) SetStatus(status choices.Status) {
	b.status = status
}

func (b *Block) Status() choices.Status {
	return b.status
}

func (b *Block) Parent() ids.ID {
	var id ids.ID
	parentHash := b.tmBlock.Header.LastBlockID.Hash
	copy(id[:], parentHash)

	return id
}

func (b *Block) Verify(context.Context) error {
	if b == nil || b.tmBlock == nil {
		return errInvalidBlock
	}

	return b.tmBlock.ValidateBasic()
}

func (b *Block) Bytes() []byte {
	block, err := b.tmBlock.ToProto()
	if err != nil {
		panic(err)
	}
	data, err := block.Marshal()
	if err != nil {
		panic(err)
	}

	return data
}

func (b *Block) Height() uint64 {
	return uint64(b.tmBlock.Height)
}

func (b *Block) Timestamp() time.Time {
	return b.tmBlock.Time
}
