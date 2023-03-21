package vm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
)

func TestABCIInfo(t *testing.T) {
	_, service := mustNewTestVm(t)
	reply := new(ctypes.ResultABCIInfo)
	assert.NoError(t, service.ABCIInfo(nil, nil, reply))
	assert.Equal(t, uint64(0), reply.Response.AppVersion)
	assert.Equal(t, int64(0), reply.Response.LastBlockHeight)
	assert.Equal(t, []uint8([]byte(nil)), reply.Response.LastBlockAppHash)
	t.Logf("%+v", reply)
}

func TestABCIQuery(t *testing.T) {
	vm, service := mustNewTestVm(t)
	k, v, tx := MakeTxKV()

	replyBroadcast := new(ctypes.ResultBroadcastTxCommit)
	require.NoError(t, service.BroadcastTxCommit(nil, &BroadcastTxArgs{tx}, replyBroadcast))

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
}
