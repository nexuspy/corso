package testdata

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// StubRootCmd builds a stub cobra command to be used as
// the root command for integration testing on the CLI
func StubRootCmd(args ...string) *cobra.Command {
	id := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	cmdArg := "testing-corso"
	c := &cobra.Command{
		Use:   cmdArg,
		Short: id,
		Long:  id + " - " + now,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "test command args: %+v", args)
			return nil
		},
	}
	c.SetArgs(args)

	return c
}
