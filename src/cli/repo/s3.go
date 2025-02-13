package repo

import (
	"strings"

	"github.com/alcionai/clues"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/alcionai/corso/src/cli/config"
	"github.com/alcionai/corso/src/cli/flags"
	. "github.com/alcionai/corso/src/cli/print"
	"github.com/alcionai/corso/src/cli/utils"
	"github.com/alcionai/corso/src/internal/events"
	"github.com/alcionai/corso/src/pkg/repository"
	"github.com/alcionai/corso/src/pkg/storage"
)

// called by repo.go to map subcommands to provider-specific handling.
func addS3Commands(cmd *cobra.Command) *cobra.Command {
	var c *cobra.Command

	switch cmd.Use {
	case initCommand:
		init := s3InitCmd()
		flags.AddRetentionConfigFlags(init)
		c, _ = utils.AddCommand(cmd, init)

	case connectCommand:
		c, _ = utils.AddCommand(cmd, s3ConnectCmd())
	}

	c.Use = c.Use + " " + s3ProviderCommandUseSuffix
	c.SetUsageTemplate(cmd.UsageTemplate())

	flags.AddAWSCredsFlags(c)
	flags.AddAzureCredsFlags(c)
	flags.AddCorsoPassphaseFlags(c)
	flags.AddS3BucketFlags(c)

	return c
}

const (
	s3ProviderCommand          = "s3"
	s3ProviderCommandUseSuffix = "--bucket <bucket>"
)

const (
	s3ProviderCommandInitExamples = `# Create a new Corso repo in AWS S3 bucket named "my-bucket"
corso repo init s3 --bucket my-bucket

# Create a new Corso repo in AWS S3 bucket named "my-bucket" using a prefix
corso repo init s3 --bucket my-bucket --prefix my-prefix

# Create a new Corso repo in an S3 compliant storage provider
corso repo init s3 --bucket my-bucket --endpoint my-s3-server-endpoint`

	s3ProviderCommandConnectExamples = `# Connect to a Corso repo in AWS S3 bucket named "my-bucket"
corso repo connect s3 --bucket my-bucket

# Connect to a Corso repo in AWS S3 bucket named "my-bucket" using a prefix
corso repo connect s3 --bucket my-bucket --prefix my-prefix

# Connect to a Corso repo in an S3 compliant storage provider
corso repo connect s3 --bucket my-bucket --endpoint my-s3-server-endpoint`
)

// ---------------------------------------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------------------------------------

// `corso repo init s3 [<flag>...]`
func s3InitCmd() *cobra.Command {
	return &cobra.Command{
		Use:     s3ProviderCommand,
		Short:   "Initialize a S3 repository",
		Long:    `Bootstraps a new S3 repository and connects it to your m365 account.`,
		RunE:    initS3Cmd,
		Args:    cobra.NoArgs,
		Example: s3ProviderCommandInitExamples,
	}
}

// initializes a s3 repo.
func initS3Cmd(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	cfg, err := config.GetConfigRepoDetails(
		ctx,
		storage.ProviderS3,
		true,
		false,
		flags.S3FlagOverrides(cmd))
	if err != nil {
		return Only(ctx, err)
	}

	opt := utils.ControlWithConfig(cfg)

	retentionOpts, err := utils.MakeRetentionOpts(cmd)
	if err != nil {
		return Only(ctx, err)
	}

	// SendStartCorsoEvent uses distict ID as tenant ID because repoID is still not generated
	utils.SendStartCorsoEvent(
		ctx,
		cfg.Storage,
		cfg.Account.ID(),
		map[string]any{"command": "init repo"},
		cfg.Account.ID(),
		opt)

	sc, err := cfg.Storage.StorageConfig()
	if err != nil {
		return Only(ctx, clues.Wrap(err, "Retrieving s3 configuration"))
	}

	s3Cfg := sc.(*storage.S3Config)

	if strings.HasPrefix(s3Cfg.Endpoint, "http://") || strings.HasPrefix(s3Cfg.Endpoint, "https://") {
		invalidEndpointErr := "endpoint doesn't support specifying protocol. " +
			"pass --disable-tls flag to use http:// instead of default https://"

		return Only(ctx, clues.New(invalidEndpointErr))
	}

	m365, err := cfg.Account.M365Config()
	if err != nil {
		return Only(ctx, clues.Wrap(err, "Failed to parse m365 account config"))
	}

	r, err := repository.Initialize(
		ctx,
		cfg.Account,
		cfg.Storage,
		opt,
		retentionOpts)
	if err != nil {
		if flags.SucceedIfExistsFV && errors.Is(err, repository.ErrorRepoAlreadyExists) {
			return nil
		}

		return Only(ctx, clues.Wrap(err, "Failed to initialize a new S3 repository"))
	}

	defer utils.CloseRepo(ctx, r)

	Infof(ctx, "Initialized a S3 repository within bucket %s.", s3Cfg.Bucket)

	if err = config.WriteRepoConfig(ctx, s3Cfg, m365, opt.Repo, r.GetID()); err != nil {
		return Only(ctx, clues.Wrap(err, "Failed to write repository configuration"))
	}

	return nil
}

// ---------------------------------------------------------------------------------------------------------
// Connect
// ---------------------------------------------------------------------------------------------------------

// `corso repo connect s3 [<flag>...]`
func s3ConnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:     s3ProviderCommand,
		Short:   "Connect to a S3 repository",
		Long:    `Ensures a connection to an existing S3 repository.`,
		RunE:    connectS3Cmd,
		Args:    cobra.NoArgs,
		Example: s3ProviderCommandConnectExamples,
	}
}

// connects to an existing s3 repo.
func connectS3Cmd(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	cfg, err := config.GetConfigRepoDetails(
		ctx,
		storage.ProviderS3,
		true,
		true,
		flags.S3FlagOverrides(cmd))
	if err != nil {
		return Only(ctx, err)
	}

	repoID := cfg.RepoID
	if len(repoID) == 0 {
		repoID = events.RepoIDNotFound
	}

	sc, err := cfg.Storage.StorageConfig()
	if err != nil {
		return Only(ctx, clues.Wrap(err, "Retrieving s3 configuration"))
	}

	s3Cfg := sc.(*storage.S3Config)

	m365, err := cfg.Account.M365Config()
	if err != nil {
		return Only(ctx, clues.Wrap(err, "Failed to parse m365 account config"))
	}

	if strings.HasPrefix(s3Cfg.Endpoint, "http://") || strings.HasPrefix(s3Cfg.Endpoint, "https://") {
		invalidEndpointErr := "endpoint doesn't support specifying protocol. " +
			"pass --disable-tls flag to use http:// instead of default https://"

		return Only(ctx, clues.New(invalidEndpointErr))
	}

	opts := utils.ControlWithConfig(cfg)

	r, err := repository.ConnectAndSendConnectEvent(
		ctx,
		cfg.Account,
		cfg.Storage,
		repoID,
		opts)
	if err != nil {
		return Only(ctx, clues.Wrap(err, "Failed to connect to the S3 repository"))
	}

	defer utils.CloseRepo(ctx, r)

	Infof(ctx, "Connected to S3 bucket %s.", s3Cfg.Bucket)

	if err = config.WriteRepoConfig(ctx, s3Cfg, m365, opts.Repo, r.GetID()); err != nil {
		return Only(ctx, clues.Wrap(err, "Failed to write repository configuration"))
	}

	return nil
}
