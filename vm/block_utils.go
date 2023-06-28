package vm

import (
	"errors"
	"fmt"
	"time"

	abci "github.com/consideritdone/landslidecore/abci/types"
	"github.com/consideritdone/landslidecore/crypto"
	"github.com/consideritdone/landslidecore/libs/log"
	mempl "github.com/consideritdone/landslidecore/mempool"
	tmstate "github.com/consideritdone/landslidecore/proto/tendermint/state"
	"github.com/consideritdone/landslidecore/proxy"
	"github.com/consideritdone/landslidecore/state"
	"github.com/consideritdone/landslidecore/types"
)

func makeCommitMock(height int64, timestamp time.Time) *types.Commit {
	var commitSig []types.CommitSig = nil
	if height != 1 {
		commitSig = []types.CommitSig{{Timestamp: time.Now()}}
	}
	return types.NewCommit(
		height,
		0,
		types.BlockID{
			Hash: []byte(""),
			PartSetHeader: types.PartSetHeader{
				Hash:  []byte(""),
				Total: 1,
			},
		},
		commitSig,
	)
}

func validateBlock(state state.State, block *types.Block) error {
	// Validate internal consistency.
	if err := block.ValidateBasic(); err != nil {
		return err
	}

	// Validate basic info.
	if block.Version.App != state.Version.Consensus.App ||
		block.Version.Block != state.Version.Consensus.Block {
		return fmt.Errorf("wrong Block.Header.Version. Expected %v, got %v",
			state.Version.Consensus,
			block.Version,
		)
	}
	if block.ChainID != state.ChainID {
		return fmt.Errorf("wrong Block.Header.ChainID. Expected %v, got %v",
			state.ChainID,
			block.ChainID,
		)
	}

	// Validate block LastCommit.
	if block.Height == state.InitialHeight {
		if len(block.LastCommit.Signatures) != 0 {
			return errors.New("initial block can't have LastCommit signatures")
		}
	}

	// NOTE: We can't actually verify it's the right proposer because we don't
	// know what round the block was first proposed. So just check that it's
	// a legit address and a known validator.
	if len(block.ProposerAddress) != crypto.AddressSize {
		return fmt.Errorf("expected ProposerAddress size %d, got %d",
			crypto.AddressSize,
			len(block.ProposerAddress),
		)
	}

	// Validate block Time
	switch {
	case block.Height > state.InitialHeight:
		if !(block.Time.After(state.LastBlockTime) || block.Time.Equal(state.LastBlockTime)) {
			return fmt.Errorf("block time %v not greater than or equal to last block time %v",
				block.Time,
				state.LastBlockTime,
			)
		}

	case block.Height == state.InitialHeight:
		genesisTime := state.LastBlockTime
		if !block.Time.Equal(genesisTime) {
			return fmt.Errorf("block time %v is not equal to genesis time %v",
				block.Time,
				genesisTime,
			)
		}

	default:
		return fmt.Errorf("block height %v lower than initial height %v",
			block.Height, state.InitialHeight)
	}

	return nil
}

func execBlockOnProxyApp(
	logger log.Logger,
	proxyAppConn proxy.AppConnConsensus,
	block *types.Block,
	store state.Store,
	initialHeight int64,
) (*tmstate.ABCIResponses, error) {
	validTxs, invalidTxs := 0, 0

	txIndex := 0
	abciResponses := new(tmstate.ABCIResponses)
	dtxs := make([]*abci.ResponseDeliverTx, len(block.Txs))
	abciResponses.DeliverTxs = dtxs

	// Execute transactions and get hash.
	proxyCb := func(req *abci.Request, res *abci.Response) {
		if r, ok := res.Value.(*abci.Response_DeliverTx); ok {
			// TODO: make use of res.Log
			// TODO: make use of this info
			// Blocks may include invalid txs.
			txRes := r.DeliverTx
			if txRes.Code == abci.CodeTypeOK {
				validTxs++
			} else {
				logger.Debug("invalid tx", "code", txRes.Code, "log", txRes.Log)
				invalidTxs++
			}

			abciResponses.DeliverTxs[txIndex] = txRes
			txIndex++
		}
	}
	proxyAppConn.SetResponseCallback(proxyCb)

	commitInfo := getBeginBlockValidatorInfo(block, store, initialHeight)

	byzVals := make([]abci.Evidence, 0)
	for _, evidence := range block.Evidence.Evidence {
		byzVals = append(byzVals, evidence.ABCI()...)
	}

	// Begin block
	var err error
	pbh := block.Header.ToProto()
	if pbh == nil {
		return nil, errors.New("nil header")
	}

	abciResponses.BeginBlock, err = proxyAppConn.BeginBlockSync(abci.RequestBeginBlock{
		Hash:                block.Hash(),
		Header:              *pbh,
		LastCommitInfo:      commitInfo,
		ByzantineValidators: byzVals,
	})
	if err != nil {
		logger.Error("error in proxyAppConn.BeginBlock", "err", err)
		return nil, err
	}

	// run txs of block
	for _, tx := range block.Txs {
		proxyAppConn.DeliverTxAsync(abci.RequestDeliverTx{Tx: tx})
		if err := proxyAppConn.Error(); err != nil {
			return nil, err
		}
	}

	// End block.
	abciResponses.EndBlock, err = proxyAppConn.EndBlockSync(abci.RequestEndBlock{Height: block.Height})
	if err != nil {
		logger.Error("error in proxyAppConn.EndBlock", "err", err)
		return nil, err
	}

	logger.Info("executed block", "height", block.Height, "num_valid_txs", validTxs, "num_invalid_txs", invalidTxs)
	return abciResponses, nil
}

func getBeginBlockValidatorInfo(block *types.Block, store state.Store, initialHeight int64) abci.LastCommitInfo {
	voteInfos := make([]abci.VoteInfo, block.LastCommit.Size())
	return abci.LastCommitInfo{
		Round: block.LastCommit.Round,
		Votes: voteInfos,
	}
}

func ABCIResponsesResultsHash(ar *tmstate.ABCIResponses) []byte {
	return types.NewResults(ar.DeliverTxs).Hash()
}

func updateState(
	st state.State,
	blockID types.BlockID,
	header *types.Header,
	abciResponses *tmstate.ABCIResponses,
) (state.State, error) {
	return state.State{
		Version:         st.Version,
		ChainID:         st.ChainID,
		InitialHeight:   st.InitialHeight,
		LastBlockHeight: header.Height,
		LastBlockID:     blockID,
		LastBlockTime:   header.Time,
		LastResultsHash: ABCIResponsesResultsHash(abciResponses),
		AppHash:         nil,
	}, nil
}

// TxPreCheck returns a function to filter transactions before processing.
// The function limits the size of a transaction to the block's maximum data size.
func TxPreCheck(state state.State) mempl.PreCheckFunc {
	maxDataBytes := types.MaxDataBytesNoEvidence(
		22020096,
		1,
	)
	return mempl.PreCheckMaxBytes(maxDataBytes)
}

// TxPostCheck returns a function to filter transactions after processing.
// The function limits the gas wanted by a transaction to the block's maximum total gas.
func TxPostCheck(state state.State) mempl.PostCheckFunc {
	return mempl.PostCheckMaxGas(-1)
}

func fireEvents(
	logger log.Logger,
	eventBus types.BlockEventPublisher,
	block *types.Block,
	abciResponses *tmstate.ABCIResponses,
) {
	if err := eventBus.PublishEventNewBlock(types.EventDataNewBlock{
		Block:            block,
		ResultBeginBlock: *abciResponses.BeginBlock,
		ResultEndBlock:   *abciResponses.EndBlock,
	}); err != nil {
		logger.Error("failed publishing new block", "err", err)
	}

	if err := eventBus.PublishEventNewBlockHeader(types.EventDataNewBlockHeader{
		Header:           block.Header,
		NumTxs:           int64(len(block.Txs)),
		ResultBeginBlock: *abciResponses.BeginBlock,
		ResultEndBlock:   *abciResponses.EndBlock,
	}); err != nil {
		logger.Error("failed publishing new block header", "err", err)
	}

	if len(block.Evidence.Evidence) != 0 {
		for _, ev := range block.Evidence.Evidence {
			if err := eventBus.PublishEventNewEvidence(types.EventDataNewEvidence{
				Evidence: ev,
				Height:   block.Height,
			}); err != nil {
				logger.Error("failed publishing new evidence", "err", err)
			}
		}
	}

	for i, tx := range block.Data.Txs {
		if err := eventBus.PublishEventTx(types.EventDataTx{TxResult: abci.TxResult{
			Height: block.Height,
			Index:  uint32(i),
			Tx:     tx,
			Result: *(abciResponses.DeliverTxs[i]),
		}}); err != nil {
			logger.Error("failed publishing event TX", "err", err)
		}
	}
}
