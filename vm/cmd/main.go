package main

import (
	"context"
	"fmt"
	"github.com/tendermint/tendermint/abci/example/counter"
	landslideCoreVM "github.com/tendermint/tendermint/vm"
	"os"

	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/ulimit"
	"github.com/ava-labs/avalanchego/vms/rpcchainvm"
)

func main() {

	if err := ulimit.Set(ulimit.DefaultFDLimit, logging.NoLog{}); err != nil {
		fmt.Printf("failed to set fd limit correctly due to: %s", err)
		os.Exit(1)
	}

	vm := landslideCoreVM.NewVM(counter.NewApplication(true))

	rpcchainvm.Serve(context.Background(), vm)
}
