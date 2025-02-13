package restore

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/alcionai/corso/src/cli/flags"
	"github.com/alcionai/corso/src/cli/utils"
)

// called by restore.go to map subcommands to provider-specific handling.
func addExchangeCommands(cmd *cobra.Command) *cobra.Command {
	var (
		c  *cobra.Command
		fs *pflag.FlagSet
	)

	switch cmd.Use {
	case restoreCommand:
		c, fs = utils.AddCommand(cmd, exchangeRestoreCmd())

		c.Use = c.Use + " " + exchangeServiceCommandUseSuffix

		// Flags addition ordering should follow the order we want them to appear in help and docs:
		// More generic (ex: --user) and more frequently used flags take precedence.
		// general flags
		fs.SortFlags = false

		flags.AddBackupIDFlag(c, true)
		flags.AddExchangeDetailsAndRestoreFlags(c)
		flags.AddRestoreConfigFlags(c)
		flags.AddFailFastFlag(c)
		flags.AddCorsoPassphaseFlags(c)
		flags.AddAWSCredsFlags(c)
		flags.AddAzureCredsFlags(c)
	}

	return c
}

const (
	exchangeServiceCommand          = "exchange"
	exchangeServiceCommandUseSuffix = "--backup <backupId>"

	//nolint:lll
	exchangeServiceCommandRestoreExamples = `# Restore emails with ID 98765abcdef and 12345abcdef from Alice's last backup (1234abcd...)
corso restore exchange --backup 1234abcd-12ab-cd34-56de-1234abcd --email 98765abcdef,12345abcdef

# Restore emails with subject containing "Hello world" in the "Inbox"
corso restore exchange --backup 1234abcd-12ab-cd34-56de-1234abcd \
    --email-subject "Hello world" --email-folder Inbox

# Restore an entire calendar
corso restore exchange --backup 1234abcd-12ab-cd34-56de-1234abcd \
    --event-calendar Calendar

# Restore the contact with ID abdef0101
corso restore exchange --backup 1234abcd-12ab-cd34-56de-1234abcd --contact abdef0101`
)

// `corso restore exchange [<flag>...]`
func exchangeRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:     exchangeServiceCommand,
		Short:   "Restore M365 Exchange service data",
		RunE:    restoreExchangeCmd,
		Args:    cobra.NoArgs,
		Example: exchangeServiceCommandRestoreExamples,
	}
}

// processes an exchange service restore.
func restoreExchangeCmd(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	if utils.HasNoFlagsAndShownHelp(cmd) {
		return nil
	}

	opts := utils.MakeExchangeOpts(cmd)

	if flags.RunModeFV == flags.RunModeFlagTest {
		return nil
	}

	if err := utils.ValidateExchangeRestoreFlags(flags.BackupIDFV, opts); err != nil {
		return err
	}

	sel := utils.IncludeExchangeRestoreDataSelectors(opts)
	utils.FilterExchangeRestoreInfoSelectors(sel, opts)

	return runRestore(
		ctx,
		cmd,
		opts.RestoreCfg,
		sel.Selector,
		flags.BackupIDFV,
		"Exchange")
}
