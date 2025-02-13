package main

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/alcionai/clues"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"

	"github.com/alcionai/corso/src/cmd/sanity_test/common"
	"github.com/alcionai/corso/src/cmd/sanity_test/export"
	"github.com/alcionai/corso/src/cmd/sanity_test/restore"
	"github.com/alcionai/corso/src/internal/m365/graph"
	"github.com/alcionai/corso/src/internal/tester/tconfig"
	"github.com/alcionai/corso/src/pkg/logger"
)

func main() {
	ls := logger.Settings{
		File:        logger.GetLogFile(""),
		Level:       logger.LLInfo,
		PIIHandling: logger.PIIPlainText,
	}

	ctx, log := logger.Seed(context.Background(), ls)
	defer func() {
		_ = log.Sync() // flush all logs in the buffer
	}()

	graph.InitializeConcurrencyLimiter(ctx, true, 4)

	adapter, err := graph.CreateAdapter(
		tconfig.GetM365TenantID(ctx),
		os.Getenv("AZURE_CLIENT_ID"),
		os.Getenv("AZURE_CLIENT_SECRET"))
	if err != nil {
		common.Fatal(ctx, "creating adapter", err)
	}

	var (
		client           = msgraphsdk.NewGraphServiceClient(adapter)
		testUser         = tconfig.GetM365UserID(ctx)
		testSite         = tconfig.GetM365SiteID(ctx)
		testKind         = os.Getenv("SANITY_TEST_KIND") // restore or export (cli arg?)
		testService      = os.Getenv("SANITY_TEST_SERVICE")
		folder           = strings.TrimSpace(os.Getenv("SANITY_TEST_FOLDER"))
		dataFolder       = os.Getenv("TEST_DATA")
		baseBackupFolder = os.Getenv("BASE_BACKUP")
	)

	ctx = clues.Add(
		ctx,
		"resource_owner", testUser,
		"service", testService,
		"sanity_restore_folder", folder)

	logger.Ctx(ctx).Info("starting sanity test check")

	switch testKind {
	case "restore":
		startTime, _ := common.MustGetTimeFromName(ctx, folder)
		clues.Add(ctx, "sanity_restore_start_time", startTime.Format(time.RFC3339))

		switch testService {
		case "exchange":
			restore.CheckEmailRestoration(ctx, client, testUser, folder, dataFolder, baseBackupFolder, startTime)
		case "onedrive":
			restore.CheckOneDriveRestoration(ctx, client, testUser, folder, dataFolder, startTime)
		case "sharepoint":
			restore.CheckSharePointRestoration(ctx, client, testSite, testUser, folder, dataFolder, startTime)
		default:
			common.Fatal(ctx, "unknown service for restore sanity tests", nil)
		}
	case "export":
		switch testService {
		case "onedrive":
			export.CheckOneDriveExport(ctx, client, testUser, folder, dataFolder)
		case "sharepoint":
			export.CheckSharePointExport(ctx, client, testSite, folder, dataFolder)
		default:
			common.Fatal(ctx, "unknown service for export sanity tests", nil)
		}
	default:
		common.Fatal(ctx, "unknown test kind (expected restore or export)", nil)
	}
}
