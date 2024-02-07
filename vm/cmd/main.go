package main

import (
	"context"
	"fmt"
	"os"

	"github.com/consideritdone/landslidecore/abci/example/counter"
	"github.com/consideritdone/landslidecore/vm"

	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/ulimit"
	"github.com/ava-labs/avalanchego/vms/rpcchainvm"
)

func main() {
	if err := ulimit.Set(ulimit.DefaultFDLimit, logging.NoLog{}); err != nil {
		fmt.Printf("failed to set fd limit correctly due to: %s", err)
		os.Exit(1)
	}

	//TODO: hardcoded PROXY app, explain the necessity
	landslideVM := vm.New(vm.LocalAppCreator(counter.NewApplication(true)))

	rpcchainvm.Serve(
		context.Background(),
		landslideVM,
	)
}
