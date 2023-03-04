package vm

import (
	"context"
	"fmt"
	"github.com/ava-labs/avalanchego/database/manager"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/consensus/snowman"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/snow/engine/snowman/block"
	"github.com/ava-labs/avalanchego/version"
	"github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/node"
	"github.com/tendermint/tendermint/proxy"
	"time"
)

var (
	_ block.ChainVM = &VM{}
)

type VM struct {
	ctx *snow.Context

	tmLogger log.Logger

	// Tendermint Application
	app types.Application

	// Tendermint proxy app
	proxyApp proxy.AppConns
}

func NewVM(app types.Application) *VM {
	return &VM{app: app}
}

func (vm *VM) Initialize(
	_ context.Context,
	chainCtx *snow.Context,
	dbManager manager.Manager,
	genesisBytes []byte,
	upgradeBytes []byte,
	configBytes []byte,
	toEngine chan<- common.Message,
	fxs []*common.Fx,
	appSender common.AppSender,
) error {
	vm.ctx = chainCtx
	vm.tmLogger = log.NewTMLogger(vm.ctx.Log)

	// Create the proxyApp and establish connections to the ABCI app (consensus, mempool, query).
	proxyApp, err := node.CreateAndStartProxyAppConns(proxy.NewLocalClientCreator(vm.app), vm.tmLogger)
	if err != nil {
		return fmt.Errorf("failed to create and start proxy app: %w ", err)
	}
	vm.proxyApp = proxyApp

	return nil
}

func (vm *VM) AppGossip(_ context.Context, nodeID ids.NodeID, msg []byte) error {
	return nil
}

func (V VM) SetState(ctx context.Context, state snow.State) error {
	//TODO implement me
	panic("implement me")
}

func (V VM) Shutdown(ctx context.Context) error {
	//TODO implement me
	panic("implement me")
}

func (V VM) Version(ctx context.Context) (string, error) {
	//TODO implement me
	panic("implement me")
}

func (V VM) CreateStaticHandlers(ctx context.Context) (map[string]*common.HTTPHandler, error) {
	//TODO implement me
	panic("implement me")
}

func (V VM) CreateHandlers(ctx context.Context) (map[string]*common.HTTPHandler, error) {
	//TODO implement me
	panic("implement me")
}

func (V VM) GetBlock(ctx context.Context, blkID ids.ID) (snowman.Block, error) {
	//TODO implement me
	panic("implement me")
}

func (V VM) ParseBlock(ctx context.Context, blockBytes []byte) (snowman.Block, error) {
	//TODO implement me
	panic("implement me")
}

func (V VM) BuildBlock(ctx context.Context) (snowman.Block, error) {
	//TODO implement me
	panic("implement me")
}

func (V VM) SetPreference(ctx context.Context, blkID ids.ID) error {
	//TODO implement me
	panic("implement me")
}

func (V VM) LastAccepted(ctx context.Context) (ids.ID, error) {
	//TODO implement me
	panic("implement me")
}

func (vm *VM) AppRequest(_ context.Context, nodeID ids.NodeID, requestID uint32, time time.Time, request []byte) error {
	return nil
}

// This VM doesn't (currently) have any app-specific messages
func (vm *VM) AppResponse(_ context.Context, nodeID ids.NodeID, requestID uint32, response []byte) error {
	return nil
}

// This VM doesn't (currently) have any app-specific messages
func (vm *VM) AppRequestFailed(_ context.Context, nodeID ids.NodeID, requestID uint32) error {
	return nil
}

func (vm *VM) CrossChainAppRequest(_ context.Context, _ ids.ID, _ uint32, deadline time.Time, request []byte) error {
	return nil
}

func (vm *VM) CrossChainAppRequestFailed(_ context.Context, _ ids.ID, _ uint32) error {
	return nil
}

func (vm *VM) CrossChainAppResponse(_ context.Context, _ ids.ID, _ uint32, response []byte) error {
	return nil
}

func (vm *VM) Connected(_ context.Context, id ids.NodeID, nodeVersion *version.Application) error {
	return nil // noop
}

func (vm *VM) Disconnected(_ context.Context, id ids.NodeID) error {
	return nil // noop
}

func (vm *VM) HealthCheck(ctx context.Context) (interface{}, error) { return nil, nil }
