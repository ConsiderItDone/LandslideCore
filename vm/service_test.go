package vm

import (
	"context"
	rpctypes "github.com/consideritdone/landslidecore/rpc/jsonrpc/types"
	"testing"
	"time"

	atypes "github.com/consideritdone/landslidecore/abci/types"
	"github.com/davecgh/go-spew/spew"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestABCIService(t *testing.T) {
	vm, service, _ := mustNewKVTestVm(t)

	t.Run("ABCIInfo", func(t *testing.T) {
		reply, err := service.ABCIInfo(&rpctypes.Context{})
		require.NoError(t, err)
		assert.Equal(t, uint64(1), reply.Response.AppVersion)
		assert.Equal(t, int64(0), reply.Response.LastBlockHeight)
		assert.Equal(t, []uint8([]byte(nil)), reply.Response.LastBlockAppHash)
		t.Logf("%+v", reply)
	})

	t.Run("ABCIQuery", func(t *testing.T) {
		k, v, tx := MakeTxKV()

		_, err := service.BroadcastTxSync(&rpctypes.Context{}, tx)
		require.NoError(t, err)

		blk, err := vm.BuildBlock(context.Background())
		require.NoError(t, err)
		require.NotNil(t, blk)

		err = blk.Accept(context.Background())
		require.NoError(t, err)

		reply, err := service.ABCIQuery(&rpctypes.Context{}, "/key", k, 0, false)
		if assert.Nil(t, err) && assert.True(t, reply.Response.IsOK()) {
			assert.EqualValues(t, v, reply.Response.Value)
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
		reply, err := service.BroadcastTxCommit(&rpctypes.Context{}, tx)
		assert.NoError(t, err)
		assert.True(t, reply.CheckTx.IsOK())
		assert.True(t, reply.DeliverTx.IsOK())
		assert.Equal(t, 0, vm.mempool.Size())
	})

	t.Run("BroadcastTxAsync", func(t *testing.T) {
		defer vm.mempool.Flush()

		initMempoolSize := vm.mempool.Size()
		_, _, tx := MakeTxKV()

		reply, err := service.BroadcastTxAsync(&rpctypes.Context{}, tx)
		assert.NoError(t, err)
		assert.NotNil(t, reply.Hash)
		assert.Equal(t, initMempoolSize+1, vm.mempool.Size())
		assert.EqualValues(t, tx, vm.mempool.ReapMaxTxs(-1)[0])
	})

	t.Run("BroadcastTxSync", func(t *testing.T) {
		defer vm.mempool.Flush()

		initMempoolSize := vm.mempool.Size()
		_, _, tx := MakeTxKV()

		reply, err := service.BroadcastTxSync(&rpctypes.Context{}, tx)
		assert.NoError(t, err)
		assert.Equal(t, reply.Code, atypes.CodeTypeOK)
		assert.Equal(t, initMempoolSize+1, vm.mempool.Size())
		assert.EqualValues(t, tx, vm.mempool.ReapMaxTxs(-1)[0])
	})
}

func TestHistoryService(t *testing.T) {
	vm, service, _ := mustNewCounterTestVm(t)

	txReply, err := service.BroadcastTxSync(&rpctypes.Context{}, []byte{0x00})
	assert.NoError(t, err)
	assert.Equal(t, atypes.CodeTypeOK, txReply.Code)

	blk, err := vm.BuildBlock(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, blk)
	assert.NoError(t, blk.Accept(context.Background()))

	t.Run("BlockchainInfo", func(t *testing.T) {
		reply, err := service.BlockchainInfo(&rpctypes.Context{}, 1, 100)
		assert.NoError(t, err)
		assert.Equal(t, int64(1), reply.LastHeight)
	})

	t.Run("Genesis", func(t *testing.T) {
		reply, err := service.Genesis(&rpctypes.Context{})
		assert.NoError(t, err)
		assert.Equal(t, vm.genesis, reply.Genesis)
	})
}

func TestNetworkService(t *testing.T) {
	vm, service, _ := mustNewCounterTestVm(t)

	t.Run("NetInfo", func(t *testing.T) {
		_, err := service.NetInfo(&rpctypes.Context{})
		assert.NoError(t, err)
	})

	t.Run("DumpConsensusState", func(t *testing.T) {
		_, err := service.DumpConsensusState(&rpctypes.Context{})
		assert.NoError(t, err)
	})

	t.Run("ConsensusState", func(t *testing.T) {
		_, err := service.ConsensusState(&rpctypes.Context{})
		assert.NoError(t, err)
	})

	t.Run("ConsensusParams", func(t *testing.T) {
		reply, err := service.ConsensusParams(&rpctypes.Context{}, nil)
		assert.NoError(t, err)
		assert.Equal(t, int64(0), reply.BlockHeight)

		txReply, err := service.BroadcastTxSync(&rpctypes.Context{}, []byte{0x00})
		assert.NoError(t, err)
		assert.Equal(t, atypes.CodeTypeOK, txReply.Code)

		blk, err := vm.BuildBlock(context.Background())
		assert.NoError(t, err)
		assert.NotNil(t, blk)
		assert.NoError(t, blk.Accept(context.Background()))

		reply2, err := service.ConsensusParams(&rpctypes.Context{}, nil)
		assert.NoError(t, err)
		assert.Equal(t, int64(1), reply2.BlockHeight)
	})

	t.Run("Health", func(t *testing.T) {
		_, err := service.Health(&rpctypes.Context{})
		assert.NoError(t, err)
	})
}

func TestSignService(t *testing.T) {
	_, _, tx := MakeTxKV()
	vm, service, _ := mustNewKVTestVm(t)

	blk0, err := vm.BuildBlock(context.Background())
	assert.ErrorIs(t, err, errNoPendingTxs, "expecting error no txs")
	assert.Nil(t, blk0)

	txReply, err := service.BroadcastTxSync(&rpctypes.Context{}, tx)
	assert.NoError(t, err)
	assert.Equal(t, atypes.CodeTypeOK, txReply.Code)

	// build 1st block
	blk1, err := vm.BuildBlock(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, blk1)
	assert.NoError(t, blk1.Accept(context.Background()))
	height1 := int64(blk1.Height())

	t.Run("Block", func(t *testing.T) {
		replyWithoutHeight, err := service.Block(&rpctypes.Context{}, &height1)
		assert.NoError(t, err)
		if assert.NotNil(t, replyWithoutHeight.Block) {
			assert.EqualValues(t, height1, replyWithoutHeight.Block.Height)
		}

		reply, err := service.Block(&rpctypes.Context{}, &height1)
		assert.NoError(t, err)
		if assert.NotNil(t, reply.Block) {
			assert.EqualValues(t, height1, reply.Block.Height)
		}
	})

	t.Run("BlockByHash", func(t *testing.T) {
		replyWithoutHash, err := service.BlockByHash(&rpctypes.Context{}, []byte{})
		assert.NoError(t, err)
		assert.Nil(t, replyWithoutHash.Block)

		hash := blk1.ID()
		reply, err := service.BlockByHash(&rpctypes.Context{}, hash[:])
		assert.NoError(t, err)
		if assert.NotNil(t, reply.Block) {
			assert.EqualValues(t, hash[:], reply.Block.Hash().Bytes())
		}
	})

	t.Run("BlockResults", func(t *testing.T) {
		replyWithoutHeight, err := service.BlockResults(&rpctypes.Context{}, nil)
		assert.NoError(t, err)
		assert.Equal(t, height1, replyWithoutHeight.Height)

		reply, err := service.BlockResults(&rpctypes.Context{}, &height1)
		assert.NoError(t, err)
		if assert.NotNil(t, reply.TxsResults) {
			assert.Equal(t, height1, reply.Height)
		}
	})

	t.Run("Tx", func(t *testing.T) {
		time.Sleep(2 * time.Second)

		reply, err := service.Tx(&rpctypes.Context{}, txReply.Hash.Bytes(), false)
		assert.NoError(t, err)
		assert.EqualValues(t, txReply.Hash, reply.Hash)
		assert.EqualValues(t, tx, reply.Tx)
	})

	//t.Run("TxSearch", func(t *testing.T) {
	//	reply := new(ctypes.ResultTxSearch)
	//	assert.NoError(t, service.TxSearch(nil, &TxSearchArgs{Query: "tx.height>0"}, reply))
	//	assert.True(t, len(reply.Txs) > 0)
	//})

	//t.Run("BlockSearch", func(t *testing.T) {
	//	reply := new(ctypes.ResultBlockSearch)
	//	assert.NoError(t, service.BlockSearch(nil, &BlockSearchArgs{Query: "block.height>0"}, reply))
	//	assert.True(t, len(reply.Blocks) > 0)
	//})
}

func TestStatusService(t *testing.T) {
	vm, service, _ := mustNewCounterTestVm(t)

	blk0, err := vm.BuildBlock(context.Background())
	assert.ErrorIs(t, err, errNoPendingTxs, "expecting error no txs")
	assert.Nil(t, blk0)

	txReply, err := service.BroadcastTxSync(&rpctypes.Context{}, []byte{0x01})
	assert.NoError(t, err)
	assert.Equal(t, atypes.CodeTypeOK, txReply.Code)

	t.Run("Status", func(t *testing.T) {
		reply1, err := service.Status(&rpctypes.Context{})
		assert.NoError(t, err)
		assert.Equal(t, int64(0), reply1.SyncInfo.LatestBlockHeight)

		blk, err := vm.BuildBlock(context.Background())
		assert.NoError(t, err)
		assert.NotNil(t, blk)
		assert.NoError(t, blk.Accept(context.Background()))

		reply2, err := service.Status(&rpctypes.Context{})
		assert.NoError(t, err)
		assert.Equal(t, int64(1), reply2.SyncInfo.LatestBlockHeight)
	})
}

func TestMempoolService(t *testing.T) {
	vm, service, _ := mustNewCounterTestVm(t)

	blk0, err := vm.BuildBlock(context.Background())
	assert.ErrorIs(t, err, errNoPendingTxs, "expecting error no txs")
	assert.Nil(t, blk0)

	tx := []byte{0x01}
	txReply, err := service.BroadcastTxSync(&rpctypes.Context{}, []byte{0x01})
	assert.NoError(t, err)
	assert.Equal(t, atypes.CodeTypeOK, txReply.Code)

	t.Run("UnconfirmedTxs", func(t *testing.T) {
		limit := 100
		reply, err := service.UnconfirmedTxs(&rpctypes.Context{}, &limit)
		assert.NoError(t, err)
		assert.True(t, len(reply.Txs) == 1)
		assert.Equal(t, reply.Txs[0], tx)
	})

	t.Run("NumUnconfirmedTxs", func(t *testing.T) {
		reply, err := service.NumUnconfirmedTxs(&rpctypes.Context{})
		assert.NoError(t, err)
		assert.Equal(t, reply.Count, 1)
		assert.Equal(t, reply.Total, 1)
	})

	t.Run("CheckTx", func(t *testing.T) {
		reply1, err := service.CheckTx(&rpctypes.Context{}, tx)
		assert.NoError(t, err)
		t.Logf("%v\n", reply1)
		// ToDo: check reply1

		blk, err := vm.BuildBlock(context.Background())
		assert.NoError(t, err)
		assert.NotNil(t, blk)
		assert.NoError(t, blk.Accept(context.Background()))

		reply2, err := service.CheckTx(&rpctypes.Context{}, tx)
		assert.NoError(t, err)
		// ToDo: check reply2
		t.Logf("%v\n", reply2)
	})
}
