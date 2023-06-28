package main

import (
	"context"
	"fmt"
	"os"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/ulimit"
	"github.com/ava-labs/avalanchego/vms/rpcchainvm"
	"github.com/consideritdone/landslidecore/abci/example/counter"
	landslideCoreVM "github.com/consideritdone/landslidecore/vm"
)

func main() {
	if err := ulimit.Set(ulimit.DefaultFDLimit, logging.NoLog{}); err != nil {
		fmt.Printf("failed to set fd limit correctly due to: %s", err)
		os.Exit(1)
	}

	vm := landslideCoreVM.NewVM(counter.NewApplication(true))

	rpcchainvm.Serve(context.Background(), vm)
}
