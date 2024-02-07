package vm

import (
	"context"
	"fmt"
	"time"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow/choices"
	"github.com/ava-labs/avalanchego/snow/consensus/snowman"
	"github.com/consideritdone/landslidecore/types"
)

var (
	_ choices.Decidable = (*Block)(nil)
	_ snowman.Block     = (*Block)(nil)
)

type Block struct {
	*types.Block
	status choices.Status
	vm     *VM
}

func NewBlock(vm *VM, block *types.Block, status choices.Status) *Block {
	return &Block{Block: block, vm: vm, status: status}
}

// ID returns a unique ID for this element.
//
// Typically, this is implemented by using a cryptographic hash of a
// binary representation of this element. An element should return the same
// IDs upon repeated calls.
func (block *Block) ID() ids.ID {
	return ids.ID(block.Hash())
}

// Accept this element.
//
// This element will be accepted by every correct node in the network.
func (block *Block) Accept(context.Context) error {
	block.vm.log.Debug(fmt.Sprintf("Accepting block %s (%s) at height %d", block.ID().Hex(), block.ID(), block.Height()))
	block.status = choices.Accepted
	// Delete this block from verified blocks as it's accepted
	delete(block.vm.verifiedBlocks, block.ID())
	return block.vm.applyBlock(block)
}

// Reject this element.
//
// This element will not be accepted by any correct node in the network.
func (block *Block) Reject(context.Context) error {
	block.vm.log.Debug(fmt.Sprintf("Rejecting block %s (%s) at height %d", block.ID().Hex(), block.ID(), block.Height()))
	block.status = choices.Rejected
	//TODO: wrap
	// put actual block to cache, so we can directly fetch it from cache
	s.blkCache.Put(blkID, blk)

	// put wrapped block bytes into database
	s.blockDB.Put(blkID[:], wrappedBytes)
	// Delete this block from verified blocks as it's rejected
	delete(block.vm.verifiedBlocks, block.ID())
	panic("implement me")
	// Commit changes to database
	return block.vm.state.Commit()
}

// Status returns this element's current status.
//
// If Accept has been called on an element with this ID, Accepted should be
// returned. Similarly, if Reject has been called on an element with this
// ID, Rejected should be returned. If the contents of this element are
// unknown, then Unknown should be returned. Otherwise, Processing should be
// returned.
//
// TODO: Consider allowing Status to return an error.
func (block *Block) Status() choices.Status {
	return block.status
}

// Parent returns the ID of this block's parent.
func (block *Block) Parent() ids.ID {
	return ids.ID(block.LastBlockID.Hash)
}

// Verify that the state transition this block would make if accepted is
// valid. If the state transition is invalid, a non-nil error should be
// returned.
//
// It is guaranteed that the Parent has been successfully verified.
//
// If nil is returned, it is guaranteed that either Accept or Reject will be
// called on this block, unless the VM is shut down.
func (block *Block) Verify(context.Context) error {
	err := block.ValidateBasic()
	if err != nil {
		return err
	}
	// Put that block to verified blocks in memory
	block.vm.verifiedBlocks[block.ID()] = block
	return nil
}

// Bytes returns the binary representation of this block.
//
// This is used for sending blocks to peers. The bytes should be able to be
// parsed into the same block on another node.
func (block *Block) Bytes() []byte {
	b, err := block.ToProto()
	if err != nil {
		panic(fmt.Sprintf("can't convert block to proto obj: %s", err))
	}
	data, err := b.Marshal()
	if err != nil {
		panic(fmt.Sprintf("can't serialize block: %s", err))
	}
	return data
}

// Height returns the height of this block in the chain.
func (block *Block) Height() uint64 {
	return uint64(block.Block.Height)
}

// Time this block was proposed at. This value should be consistent across
// all nodes. If this block hasn't been successfully verified, any value can
// be returned. If this block is the last accepted block, the timestamp must
// be returned correctly. Otherwise, accepted blocks can return any value.
func (block *Block) Timestamp() time.Time {
	return block.Block.Time
}
