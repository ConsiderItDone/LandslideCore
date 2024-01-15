package vm

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ava-labs/avalanchego/snow/engine/common"
	tmjson "github.com/consideritdone/landslidecore/libs/json"
	ctypes "github.com/consideritdone/landslidecore/rpc/core/types"
	rpctypes "github.com/consideritdone/landslidecore/rpc/jsonrpc/types"
	"github.com/consideritdone/landslidecore/types"
	"golang.org/x/sync/errgroup"

	atypes "github.com/consideritdone/landslidecore/abci/types"
	"github.com/davecgh/go-spew/spew"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func broadcastTx(t *testing.T, v *VM, msgs chan common.Message, tx []byte) (*ctypes.ResultBroadcastTxCommit, error) {
	var result *ctypes.ResultBroadcastTxCommit
	wg := new(errgroup.Group)
	wg.Go(func() error {
		select {
		case <-msgs:
			t.Logf("found new txs in engine")
			block, err := v.BuildBlock(context.Background())
			if err != nil {
				return err
			}
			return block.Accept(context.Background())
		case <-time.After(time.Minute):
			return errors.New("timeout. no txs")
		}
	})
	wg.Go(func() error {
		var err error
		result, err = NewService(v).BroadcastTxCommit(&rpctypes.Context{}, tx)
		return err
	})
	if err := wg.Wait(); err != nil {
		return nil, err
	}
	return result, nil
}

func TestABCIService(t *testing.T) {
	vm, service, _ := mustNewKVTestVm(t)

	t.Run("ABCIInfo", func(t *testing.T) {
		reply, err := service.ABCIInfo(&rpctypes.Context{})
		require.NoError(t, err)
		assert.Equal(t, uint64(1), reply.Response.AppVersion)
		assert.Equal(t, int64(1), reply.Response.LastBlockHeight)
		assert.Equal(t, []uint8([]byte{0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0}), reply.Response.LastBlockAppHash)
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

func TestEventService(t *testing.T) {
	_, service, _ := mustNewCounterTestVm(t)

	// subscribe to new blocks and make sure height increments by 1
	t.Run("Subscribe", func(t *testing.T) {
		events := []string{
			types.QueryForEvent(types.EventNewBlock).String(),
			types.QueryForEvent(types.EventNewBlockHeader).String(),
			types.QueryForEvent(types.EventValidBlock).String(),
		}

		for i, event := range events {
			_, err := service.Subscribe(&rpctypes.Context{JSONReq: &rpctypes.RPCRequest{ID: rpctypes.JSONRPCIntID(i)}}, event)
			require.NoError(t, err)
		}
		t.Cleanup(func() {
			if _, err := service.UnsubscribeAll(&rpctypes.Context{}); err != nil {
				t.Error(err)
			}
		})
	})

	t.Run("Unsubscribe", func(t *testing.T) {
		events := []string{
			types.QueryForEvent(types.EventNewBlock).String(),
			types.QueryForEvent(types.EventNewBlockHeader).String(),
			types.QueryForEvent(types.EventValidBlock).String(),
		}

		for i, event := range events {
			_, err := service.Subscribe(&rpctypes.Context{JSONReq: &rpctypes.RPCRequest{ID: rpctypes.JSONRPCIntID(i)}}, event)
			require.NoError(t, err)
			_, err = service.Unsubscribe(&rpctypes.Context{}, event)
			require.NoError(t, err)
		}
		//TODO: investigate the need to use Cleanup with UnsubscribeAll
		//t.Cleanup(func() {
		//	if _, err := service.UnsubscribeAll(&rpctypes.Context{}); err != nil {
		//		t.Error(err)
		//	}
		//})
	})

	t.Run("UnsubscribeAll", func(t *testing.T) {
		events := []string{
			types.QueryForEvent(types.EventNewBlock).String(),
			types.QueryForEvent(types.EventNewBlockHeader).String(),
			types.QueryForEvent(types.EventValidBlock).String(),
		}

		for i, event := range events {
			_, err := service.Subscribe(&rpctypes.Context{JSONReq: &rpctypes.RPCRequest{ID: rpctypes.JSONRPCIntID(i)}}, event)
			require.NoError(t, err)
		}
		_, err := service.UnsubscribeAll(&rpctypes.Context{})
		if err != nil {
			t.Error(err)
		}
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

	t.Run("Genesis", func(t *testing.T) {
		reply, err := service.Genesis(&rpctypes.Context{})
		assert.NoError(t, err)
		assert.Equal(t, vm.genesis, reply.Genesis)
	})

	t.Run("GenesisChunked", func(t *testing.T) {
		first, err := service.GenesisChunked(&rpctypes.Context{}, 0)
		require.NoError(t, err)

		decoded := make([]string, 0, first.TotalChunks)
		for i := 0; i < first.TotalChunks; i++ {
			chunk, err := service.GenesisChunked(&rpctypes.Context{}, uint(i))
			require.NoError(t, err)
			data, err := base64.StdEncoding.DecodeString(chunk.Data)
			require.NoError(t, err)
			decoded = append(decoded, string(data))

		}
		doc := []byte(strings.Join(decoded, ""))

		var out types.GenesisDoc
		require.NoError(t, tmjson.Unmarshal(doc, &out), "first: %+v, doc: %s", first, string(doc))
	})

	t.Run("BlockchainInfo", func(t *testing.T) {
		reply, err := service.BlockchainInfo(&rpctypes.Context{}, 1, 100)
		assert.NoError(t, err)
		assert.Equal(t, int64(2), reply.LastHeight)
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
		assert.Equal(t, int64(1), reply.BlockHeight)

		txReply, err := service.BroadcastTxSync(&rpctypes.Context{}, []byte{0x00})
		assert.NoError(t, err)
		assert.Equal(t, atypes.CodeTypeOK, txReply.Code)

		blk, err := vm.BuildBlock(context.Background())
		assert.NoError(t, err)
		assert.NotNil(t, blk)
		assert.NoError(t, blk.Accept(context.Background()))

		reply2, err := service.ConsensusParams(&rpctypes.Context{}, nil)
		assert.NoError(t, err)
		assert.Equal(t, int64(2), reply2.BlockHeight)
	})

	t.Run("Health", func(t *testing.T) {
		_, err := service.Health(&rpctypes.Context{})
		assert.NoError(t, err)
	})
}

func TestSignService(t *testing.T) {
	_, _, tx := MakeTxKV()
	tx2 := []byte{0x02}
	tx3 := []byte{0x03}
	vm, service, msgs := mustNewKVTestVm(t)

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

	t.Run("TxSearch", func(t *testing.T) {
		txReply2, err := service.BroadcastTxAsync(&rpctypes.Context{}, tx2)
		assert.NoError(t, err)
		assert.Equal(t, atypes.CodeTypeOK, txReply2.Code)

		blk2, err := vm.BuildBlock(context.Background())
		require.NoError(t, err)
		assert.NotNil(t, blk2)
		assert.NoError(t, blk2.Accept(context.Background()))

		time.Sleep(time.Second)

		reply, err := service.TxSearch(&rpctypes.Context{}, fmt.Sprintf("tx.hash='%s'", txReply2.Hash), false, nil, nil, "asc")
		assert.NoError(t, err)
		assert.True(t, len(reply.Txs) > 0)

		// TODO: need to fix
		// reply2, err := service.TxSearch(&rpctypes.Context{}, fmt.Sprintf("tx.height=%d", blk2.Height()), false, nil, nil, "desc")
		// assert.NoError(t, err)
		// assert.True(t, len(reply2.Txs) > 0)
	})

	//TODO: Check logic of test
	t.Run("Commit", func(t *testing.T) {
		txReply, err := service.BroadcastTxAsync(&rpctypes.Context{}, tx3)
		require.NoError(t, err)
		assert.Equal(t, atypes.CodeTypeOK, txReply.Code)

		assert, require := assert.New(t), require.New(t)

		// get an offset of height to avoid racing and guessing
		s, err := service.Status(&rpctypes.Context{})
		require.NoError(err)
		// sh is start height or status height
		sh := s.SyncInfo.LatestBlockHeight

		// look for the future
		h := sh + 20
		_, err = service.Block(&rpctypes.Context{}, &h)
		require.Error(err) // no block yet

		// write something
		k, v, tx := MakeTxKV()
		bres, err := broadcastTx(t, vm, msgs, tx)
		require.NoError(err)
		require.True(bres.DeliverTx.IsOK())
		time.Sleep(2 * time.Second)

		txh := bres.Height
		apph := txh

		// wait before querying
		err = WaitForHeight(service, apph, nil)
		require.NoError(err)

		qres, err := service.ABCIQuery(&rpctypes.Context{}, "/key", k, 0, false)
		require.NoError(err)
		if assert.True(qres.Response.IsOK()) {
			assert.Equal(k, qres.Response.Key)
			assert.EqualValues(v, qres.Response.Value)
		}

		// make sure we can lookup the tx with proof
		ptx, err := service.Tx(&rpctypes.Context{}, bres.Hash, true)
		require.NoError(err)
		assert.EqualValues(txh, ptx.Height)
		assert.EqualValues(tx, ptx.Tx)

		// and we can even check the block is added
		block, err := service.Block(&rpctypes.Context{}, &apph)
		require.NoError(err)
		appHash := block.Block.Header.AppHash
		assert.True(len(appHash) > 0)
		assert.EqualValues(apph, block.Block.Header.Height)

		blockByHash, err := service.BlockByHash(&rpctypes.Context{}, block.BlockID.Hash)
		require.NoError(err)
		require.Equal(block, blockByHash)

		// now check the results
		blockResults, err := service.BlockResults(&rpctypes.Context{}, &txh)
		require.Nil(err, "%+v", err)
		assert.Equal(txh, blockResults.Height)
		if assert.Equal(2, len(blockResults.TxsResults)) {
			// check success code
			assert.EqualValues(0, blockResults.TxsResults[0].Code)
		}

		// check blockchain info, now that we know there is info
		info, err := service.BlockchainInfo(&rpctypes.Context{}, apph, apph)
		require.NoError(err)
		assert.True(info.LastHeight >= apph)
		if assert.Equal(1, len(info.BlockMetas)) {
			lastMeta := info.BlockMetas[0]
			assert.EqualValues(apph, lastMeta.Header.Height)
			blockData := block.Block
			assert.Equal(blockData.Header.AppHash, lastMeta.Header.AppHash)
			assert.Equal(block.BlockID, lastMeta.BlockID)
		}

		// and get the corresponding commit with the same apphash
		commit, err := service.Commit(&rpctypes.Context{}, &apph)
		require.NoError(err)
		assert.NotNil(commit)
		assert.Equal(appHash, commit.Header.AppHash)

		// compare the commits (note Commit(2) has commit from Block(3))
		h = apph - 1
		commit2, err := service.Commit(&rpctypes.Context{}, &h)
		require.NoError(err)
		assert.Equal(block.Block.LastCommitHash, commit2.Commit.Hash())

		// and we got a proof that works!
		pres, err := service.ABCIQuery(&rpctypes.Context{}, "/key", k, 0, true)
		require.NoError(err)
		assert.True(pres.Response.IsOK())
	})

	t.Run("BlockSearch", func(t *testing.T) {
		reply, err := service.BlockSearch(&rpctypes.Context{}, "block.height=2", nil, nil, "desc")
		assert.NoError(t, err)
		assert.True(t, len(reply.Blocks) > 0)
	})
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
		assert.Equal(t, int64(1), reply1.SyncInfo.LatestBlockHeight)

		blk, err := vm.BuildBlock(context.Background())
		assert.NoError(t, err)
		assert.NotNil(t, blk)
		assert.NoError(t, blk.Accept(context.Background()))

		reply2, err := service.Status(&rpctypes.Context{})
		assert.NoError(t, err)
		assert.Equal(t, int64(2), reply2.SyncInfo.LatestBlockHeight)
	})
}

func TestMempoolService(t *testing.T) {
	vm, service, _ := mustNewCounterTestVm(t)

	blk0, err := vm.BuildBlock(context.Background())
	assert.ErrorIs(t, err, errNoPendingTxs, "expecting error no txs")
	assert.Nil(t, blk0)

	tx := []byte{0x01}
	expectedTx := types.Tx(tx)
	txReply, err := service.BroadcastTxSync(&rpctypes.Context{}, []byte{0x01})
	assert.NoError(t, err)
	assert.Equal(t, atypes.CodeTypeOK, txReply.Code)

	t.Run("UnconfirmedTxs", func(t *testing.T) {
		limit := 100
		reply, err := service.UnconfirmedTxs(&rpctypes.Context{}, &limit)
		assert.NoError(t, err)
		assert.True(t, len(reply.Txs) == 1)
		assert.Equal(t, expectedTx, reply.Txs[0])
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
