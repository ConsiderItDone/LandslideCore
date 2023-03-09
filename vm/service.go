package vm

import (
	"net/http"
)

// Service is the API service for this VM
type Service struct {
	vm *VM
}

type HTTP interface {
	BroadcastTxCommit(_ *http.Request, args *ProposeBlockArgs, reply *ProposeBlockReply) error
}

// ProposeBlockArgs are the arguments to function ProposeValue
type ProposeBlockArgs struct {
	// Data in the block. Must be hex encoding of 32 bytes.
	Data string `json:"data"`
}

// ProposeBlockReply is the reply from function ProposeBlock
type ProposeBlockReply struct{ Success bool }

func (s *Service) BroadcastTxCommit(_ *http.Request, args *ProposeBlockArgs, reply *ProposeBlockReply) error {
	//bytes, err := formatting.Decode(formatting.Hex, args.Data)
	//if err != nil || len(bytes) != DataLen {
	//	return errBadData
	//}
	//reply.Success = s.vm.proposeBlock(BytesToData(bytes))
	return nil
}
