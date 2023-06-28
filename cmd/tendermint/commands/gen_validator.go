package commands

import (
	"fmt"

	tmjson "github.com/consideritdone/landslidecore/libs/json"
	"github.com/consideritdone/landslidecore/privval"
	"github.com/spf13/cobra"
)

// GenValidatorCmd allows the generation of a keypair for a
// validator.
var GenValidatorCmd = &cobra.Command{
	Use:     "gen-validator",
	Aliases: []string{"gen_validator"},
	Short:   "Generate new validator keypair",
	PreRun:  deprecateSnakeCase,
	Run:     genValidator,
}

func genValidator(cmd *cobra.Command, args []string) {
	pv := privval.GenFilePV("", "")
	jsbz, err := tmjson.Marshal(pv)
	if err != nil {
		panic(err)
	}
	fmt.Printf(`%v
`, string(jsbz))
}
