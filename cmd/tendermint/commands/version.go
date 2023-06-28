package commands

import (
	"fmt"

	"github.com/consideritdone/landslidecore/version"
	"github.com/spf13/cobra"
)

// VersionCmd ...
var VersionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version info",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(version.TMCoreSemVer)
	},
}
