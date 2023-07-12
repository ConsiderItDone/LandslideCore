package vm

import (
	"context"
	"fmt"
	"testing"
	"time"

	atypes "github.com/consideritdone/landslidecore/abci/types"
	ctypes "github.com/consideritdone/landslidecore/rpc/core/types"
	"github.com/davecgh/go-spew/spew"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestABCIService(t *testing.T) {
	vm, service, _ := mustNewKVTestVm(t)

	t.Run("ABCIInfo", func(t *testing.T) {
		reply := new(ctypes.ResultABCIInfo)
		assert.NoError(t, service.ABCIInfo(nil, nil, reply))
		assert.Equal(t, uint64(1), reply.Response.AppVersion)
		assert.Equal(t, int64(1), reply.Response.LastBlockHeight)
		assert.Equal(t, []uint8([]byte{0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0}), reply.Response.LastBlockAppHash)
		t.Logf("%+v", reply)
	})

	t.Run("ABCIQuery", func(t *testing.T) {
		k, v, tx := MakeTxKV()

		replyBroadcast := new(ctypes.ResultBroadcastTx)
		require.NoError(t, service.BroadcastTxSync(nil, &BroadcastTxArgs{tx}, replyBroadcast))

		blk, err := vm.BuildBlock(context.Background())
		require.NoError(t, err)
		require.NotNil(t, blk)

		err = blk.Accept(context.Background())
		require.NoError(t, err)

		res := new(ctypes.ResultABCIQuery)
		err = service.ABCIQuery(nil, &ABCIQueryArgs{Path: "/key", Data: k}, res)
		if assert.Nil(t, err) && assert.True(t, res.Response.IsOK()) {
			assert.EqualValues(t, v, res.Response.Value)
		}
		spew.Dump(vm.mempool.Size())
	})

	t.Run("BroadcastTxCommit", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func(ctx context.Context) {
			end := false
			for !end {
				select {
				case <-ctx.Done():
					end = true
				default:
					if vm.mempool.Size() > 0 {
						block, err := vm.BuildBlock(ctx)
						t.Logf("new block: %#v", block)
						require.NoError(t, err)
						require.NoError(t, block.Accept(ctx))
					} else {
						time.Sleep(500 * time.Millisecond)
					}
				}
			}
		}(ctx)

		_, _, tx := MakeTxKV()
		reply := new(ctypes.ResultBroadcastTxCommit)
		assert.NoError(t, service.BroadcastTxCommit(nil, &BroadcastTxArgs{tx}, reply))
		assert.True(t, reply.CheckTx.IsOK())
		assert.True(t, reply.DeliverTx.IsOK())
		assert.Equal(t, 0, vm.mempool.Size())
	})

	t.Run("BroadcastTxAsync", func(t *testing.T) {
		defer vm.mempool.Flush()

		initMempoolSize := vm.mempool.Size()
		_, _, tx := MakeTxKV()

		reply := new(ctypes.ResultBroadcastTx)
		assert.NoError(t, service.BroadcastTxAsync(nil, &BroadcastTxArgs{tx}, reply))
		assert.NotNil(t, reply.Hash)
		assert.Equal(t, initMempoolSize+1, vm.mempool.Size())
		assert.EqualValues(t, tx, vm.mempool.ReapMaxTxs(-1)[0])
	})

	t.Run("BroadcastTxSync", func(t *testing.T) {
		defer vm.mempool.Flush()

		initMempoolSize := vm.mempool.Size()
		_, _, tx := MakeTxKV()

		reply := new(ctypes.ResultBroadcastTx)
		assert.NoError(t, service.BroadcastTxSync(nil, &BroadcastTxArgs{Tx: tx}, reply))
		assert.Equal(t, reply.Code, atypes.CodeTypeOK)
		assert.Equal(t, initMempoolSize+1, vm.mempool.Size())
		assert.EqualValues(t, tx, vm.mempool.ReapMaxTxs(-1)[0])
	})
}

func TestHistoryService(t *testing.T) {
	vm, service, _ := mustNewCounterTestVm(t)

	txReply := new(ctypes.ResultBroadcastTx)
	assert.NoError(t, service.BroadcastTxSync(nil, &BroadcastTxArgs{Tx: []byte{0x00}}, txReply))
	assert.Equal(t, atypes.CodeTypeOK, txReply.Code)

	blk, err := vm.BuildBlock(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, blk)
	assert.NoError(t, blk.Accept(context.Background()))

	t.Run("BlockchainInfo", func(t *testing.T) {
		reply := new(ctypes.ResultBlockchainInfo)
		assert.NoError(t, service.BlockchainInfo(nil, &BlockchainInfoArgs{1, 100}, reply))
		assert.Equal(t, int64(2), reply.LastHeight)
	})

	t.Run("Genesis", func(t *testing.T) {
		reply := new(ctypes.ResultGenesis)
		assert.NoError(t, service.Genesis(nil, nil, reply))
		assert.Equal(t, vm.genesis, reply.Genesis)
	})
}

func TestNetworkService(t *testing.T) {
	vm, service, _ := mustNewCounterTestVm(t)

	t.Run("NetInfo", func(t *testing.T) {
		reply := new(ctypes.ResultNetInfo)
		assert.NoError(t, service.NetInfo(nil, nil, reply))
	})

	t.Run("DumpConsensusState", func(t *testing.T) {
		reply := new(ctypes.ResultDumpConsensusState)
		assert.NoError(t, service.DumpConsensusState(nil, nil, reply))
	})

	t.Run("ConsensusState", func(t *testing.T) {
		reply := new(ctypes.ResultConsensusState)
		assert.NoError(t, service.ConsensusState(nil, nil, reply))
	})

	t.Run("ConsensusParams", func(t *testing.T) {
		reply := new(ctypes.ResultConsensusParams)
		assert.NoError(t, service.ConsensusParams(nil, nil, reply))
		assert.Equal(t, int64(1), reply.BlockHeight)

		txReply := new(ctypes.ResultBroadcastTx)
		assert.NoError(t, service.BroadcastTxSync(nil, &BroadcastTxArgs{Tx: []byte{0x00}}, txReply))
		assert.Equal(t, atypes.CodeTypeOK, txReply.Code)

		blk, err := vm.BuildBlock(context.Background())
		assert.NoError(t, err)
		assert.NotNil(t, blk)
		assert.NoError(t, blk.Accept(context.Background()))

		assert.NoError(t, service.ConsensusParams(nil, nil, reply))
		assert.Equal(t, int64(2), reply.BlockHeight)
	})

	t.Run("Health", func(t *testing.T) {
		reply := new(ctypes.ResultHealth)
		assert.NoError(t, service.Health(nil, nil, reply))
	})
}

func TestSignService(t *testing.T) {
	_, _, tx := MakeTxKV()
	vm, service, _ := mustNewKVTestVm(t)

	blk0, err := vm.BuildBlock(context.Background())
	assert.ErrorIs(t, err, errNoPendingTxs, "expecting error no txs")
	assert.Nil(t, blk0)

	txArg := &BroadcastTxArgs{tx}
	txReply := new(ctypes.ResultBroadcastTx)
	err = service.BroadcastTxSync(nil, txArg, txReply)
	assert.NoError(t, err)
	assert.Equal(t, atypes.CodeTypeOK, txReply.Code)

	// build 1st block
	blk1, err := vm.BuildBlock(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, blk1)
	assert.NoError(t, blk1.Accept(context.Background()))
	height1 := int64(blk1.Height())

	t.Run("Block", func(t *testing.T) {
		replyWithoutHeight := new(ctypes.ResultBlock)
		assert.NoError(t, service.Block(nil, &BlockHeightArgs{&height1}, replyWithoutHeight))
		if assert.NotNil(t, replyWithoutHeight.Block) {
			assert.EqualValues(t, height1, replyWithoutHeight.Block.Height)
		}

		reply := new(ctypes.ResultBlock)
		assert.NoError(t, service.Block(nil, &BlockHeightArgs{Height: &height1}, reply))
		if assert.NotNil(t, reply.Block) {
			assert.EqualValues(t, height1, reply.Block.Height)
		}
	})

	t.Run("BlockByHash", func(t *testing.T) {
		replyWithoutHash := new(ctypes.ResultBlock)
		assert.NoError(t, service.BlockByHash(nil, &BlockHashArgs{}, replyWithoutHash))
		assert.Nil(t, replyWithoutHash.Block)

		reply := new(ctypes.ResultBlock)
		hash := blk1.ID()

		assert.NoError(t, service.BlockByHash(nil, &BlockHashArgs{Hash: hash[:]}, reply))
		if assert.NotNil(t, reply.Block) {
			assert.EqualValues(t, hash[:], reply.Block.Hash().Bytes())
		}
	})

	t.Run("BlockResults", func(t *testing.T) {
		replyWithoutHeight := new(ctypes.ResultBlockResults)
		assert.NoError(t, service.BlockResults(nil, &BlockHeightArgs{}, replyWithoutHeight))
		assert.Equal(t, height1, replyWithoutHeight.Height)

		reply := new(ctypes.ResultBlockResults)
		assert.NoError(t, service.BlockResults(nil, &BlockHeightArgs{Height: &height1}, reply))
		if assert.NotNil(t, reply.TxsResults) {
			assert.Equal(t, height1, reply.Height)
		}
	})

	t.Run("Tx", func(t *testing.T) {
		time.Sleep(2 * time.Second)

		reply := new(ctypes.ResultTx)
		assert.NoError(t, service.Tx(nil, &TxArgs{Hash: txReply.Hash.Bytes()}, reply))
		assert.EqualValues(t, txReply.Hash, reply.Hash)
		assert.EqualValues(t, tx, reply.Tx)
	})

	t.Run("TxSearch", func(t *testing.T) {
		reply := new(ctypes.ResultTxSearch)
		assert.NoError(t, service.TxSearch(nil, &TxSearchArgs{Query: fmt.Sprintf("tx.hash='%s'", txReply.Hash)}, reply))
		assert.True(t, len(reply.Txs) > 0)
	})

	t.Run("BlockSearch", func(t *testing.T) {
		reply := new(ctypes.ResultBlockSearch)
		assert.NoError(t, service.BlockSearch(nil, &BlockSearchArgs{Query: "block.height=2"}, reply))
		assert.True(t, len(reply.Blocks) > 0)
	})
}

func TestStatusService(t *testing.T) {
	vm, service, _ := mustNewCounterTestVm(t)

	blk0, err := vm.BuildBlock(context.Background())
	assert.ErrorIs(t, err, errNoPendingTxs, "expecting error no txs")
	assert.Nil(t, blk0)

	txArg := &BroadcastTxArgs{
		Tx: []byte{0x01},
	}
	txReply := &ctypes.ResultBroadcastTx{}
	err = service.BroadcastTxSync(nil, txArg, txReply)
	assert.NoError(t, err)
	assert.Equal(t, atypes.CodeTypeOK, txReply.Code)

	t.Run("Status", func(t *testing.T) {
		reply1 := new(ctypes.ResultStatus)
		assert.NoError(t, service.Status(nil, nil, reply1))
		assert.Equal(t, int64(1), reply1.SyncInfo.LatestBlockHeight)

		blk, err := vm.BuildBlock(context.Background())
		assert.NoError(t, err)
		assert.NotNil(t, blk)
		assert.NoError(t, blk.Accept(context.Background()))

		reply2 := new(ctypes.ResultStatus)
		assert.NoError(t, service.Status(nil, nil, reply2))
		assert.Equal(t, int64(2), reply2.SyncInfo.LatestBlockHeight)
	})
}

func TestMempoolService(t *testing.T) {
	vm, service, _ := mustNewCounterTestVm(t)

	blk0, err := vm.BuildBlock(context.Background())
	assert.ErrorIs(t, err, errNoPendingTxs, "expecting error no txs")
	assert.Nil(t, blk0)

	txArg := &BroadcastTxArgs{
		Tx: []byte{0x01},
	}
	txReply := &ctypes.ResultBroadcastTx{}
	err = service.BroadcastTxSync(nil, txArg, txReply)
	assert.NoError(t, err)
	assert.Equal(t, atypes.CodeTypeOK, txReply.Code)

	t.Run("UnconfirmedTxs", func(t *testing.T) {
		limit := 100
		reply := new(ctypes.ResultUnconfirmedTxs)
		assert.NoError(t, service.UnconfirmedTxs(nil, &UnconfirmedTxsArgs{Limit: &limit}, reply))
		assert.True(t, len(reply.Txs) == 1)
		assert.Equal(t, reply.Txs[0], txArg.Tx)
	})

	t.Run("NumUnconfirmedTxs", func(t *testing.T) {
		reply := new(ctypes.ResultUnconfirmedTxs)
		assert.NoError(t, service.NumUnconfirmedTxs(nil, nil, reply))
		assert.Equal(t, reply.Count, 1)
		assert.Equal(t, reply.Total, 1)
	})

	t.Run("CheckTx", func(t *testing.T) {
		reply1 := new(ctypes.ResultCheckTx)
		assert.NoError(t, service.CheckTx(nil, &CheckTxArgs{Tx: txArg.Tx}, reply1))
		// ToDo: check reply1

		blk, err := vm.BuildBlock(context.Background())
		assert.NoError(t, err)
		assert.NotNil(t, blk)
		assert.NoError(t, blk.Accept(context.Background()))

		reply2 := new(ctypes.ResultCheckTx)
		assert.NoError(t, service.CheckTx(nil, &CheckTxArgs{Tx: txArg.Tx}, reply2))
		// ToDo: check reply2
	})
}
