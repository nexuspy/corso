package exchange

import (
	stdpath "path"
	"testing"

	"github.com/alcionai/clues"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/alcionai/corso/src/internal/tester"
	"github.com/alcionai/corso/src/internal/tester/tconfig"
	"github.com/alcionai/corso/src/pkg/account"
	"github.com/alcionai/corso/src/pkg/control"
	"github.com/alcionai/corso/src/pkg/fault"
	"github.com/alcionai/corso/src/pkg/services/m365/api"
)

const (
	// Need to use a hard-coded ID because GetAllFolderNamesForUser only gets
	// top-level folders right now.
	//nolint:lll
	testFolderID = "AAMkAGZmNjNlYjI3LWJlZWYtNGI4Mi04YjMyLTIxYThkNGQ4NmY1MwAuAAAAAADCNgjhM9QmQYWNcI7hCpPrAQDSEBNbUIB9RL6ePDeF3FIYAABl7AqpAAA="
	//nolint:lll
	topFolderID = "AAMkAGZmNjNlYjI3LWJlZWYtNGI4Mi04YjMyLTIxYThkNGQ4NmY1MwAuAAAAAADCNgjhM9QmQYWNcI7hCpPrAQDSEBNbUIB9RL6ePDeF3FIYAAAAAAEIAAA="
	//nolint:lll
	// Full folder path for the folder above.
	expectedFolderPath = "toplevel/subFolder/subsubfolder"
)

type MailFolderCacheIntegrationSuite struct {
	tester.Suite
	credentials account.M365Config
}

func TestMailFolderCacheIntegrationSuite(t *testing.T) {
	suite.Run(t, &MailFolderCacheIntegrationSuite{
		Suite: tester.NewIntegrationSuite(
			t,
			[][]string{tconfig.M365AcctCredEnvs}),
	})
}

func (suite *MailFolderCacheIntegrationSuite) SetupSuite() {
	t := suite.T()

	a := tconfig.NewM365Account(t)
	m365, err := a.M365Config()
	require.NoError(t, err, clues.ToCore(err))

	suite.credentials = m365
}

func (suite *MailFolderCacheIntegrationSuite) TestDeltaFetch() {
	suite.T().Skipf("Test depends on hardcoded folder names. Skipping till that is fixed")

	tests := []struct {
		name string
		root string
		path []string
	}{
		{
			name: "Default Root",
			root: api.MsgFolderRoot,
		},
		{
			name: "Node Root",
			root: topFolderID,
		},
		{
			name: "Node Root Non-empty Path",
			root: topFolderID,
			path: []string{"some", "leading", "path"},
		},
	}
	userID := tconfig.M365UserID(suite.T())

	for _, test := range tests {
		suite.Run(test.name, func() {
			t := suite.T()

			ctx, flush := tester.NewContext(t)
			defer flush()

			ac, err := api.NewClient(suite.credentials, control.DefaultOptions())
			require.NoError(t, err, clues.ToCore(err))

			acm := ac.Mail()

			mfc := mailContainerCache{
				userID: userID,
				enumer: acm,
				getter: acm,
			}

			err = mfc.Populate(ctx, fault.New(true), test.root, test.path...)
			require.NoError(t, err, clues.ToCore(err))

			p, l, err := mfc.IDToPath(ctx, testFolderID)
			require.NoError(t, err, clues.ToCore(err))
			t.Logf("Path: %s\n", p.String())
			t.Logf("Location: %s\n", l.String())

			expectedPath := stdpath.Join(append(test.path, expectedFolderPath)...)
			assert.Equal(t, expectedPath, p.String())
			identifier, ok := mfc.LocationInCache(p.String())
			assert.True(t, ok)
			assert.NotEmpty(t, identifier)
		})
	}
}
