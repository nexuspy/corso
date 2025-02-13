package metadata_test

import (
	"fmt"
	"testing"

	"github.com/alcionai/clues"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	odmetadata "github.com/alcionai/corso/src/internal/m365/collection/drive/metadata"
	"github.com/alcionai/corso/src/internal/m365/graph/metadata"
	"github.com/alcionai/corso/src/internal/tester"
	"github.com/alcionai/corso/src/pkg/path"
)

type boolfAssertionFunc func(assert.TestingT, bool, string, ...any) bool

type testCase struct {
	service  path.ServiceType
	category path.CategoryType
	expected boolfAssertionFunc
}

var (
	tenant = "a-tenant"
	user   = "a-user"

	notMetaSuffixes = []string{
		"",
		odmetadata.DataFileSuffix,
	}

	metaSuffixes = []string{
		odmetadata.MetaFileSuffix,
		odmetadata.DirMetaFileSuffix,
	}

	cases = []testCase{
		{
			service:  path.ExchangeService,
			category: path.EmailCategory,
			expected: assert.Falsef,
		},
		{
			service:  path.ExchangeService,
			category: path.ContactsCategory,
			expected: assert.Falsef,
		},
		{
			service:  path.ExchangeService,
			category: path.EventsCategory,
			expected: assert.Falsef,
		},
		{
			service:  path.OneDriveService,
			category: path.FilesCategory,
			expected: assert.Truef,
		},
		{
			service:  path.SharePointService,
			category: path.LibrariesCategory,
			expected: assert.Truef,
		},
		{
			service:  path.SharePointService,
			category: path.ListsCategory,
			expected: assert.Falsef,
		},
		{
			service:  path.SharePointService,
			category: path.PagesCategory,
			expected: assert.Falsef,
		},
		{
			service:  path.GroupsService,
			category: path.LibrariesCategory,
			expected: assert.Truef,
		},
	}
)

type MetadataUnitSuite struct {
	tester.Suite
}

func TestMetadataUnitSuite(t *testing.T) {
	suite.Run(t, &MetadataUnitSuite{Suite: tester.NewUnitSuite(t)})
}

func (suite *MetadataUnitSuite) TestIsMetadataFile_Files_MetaSuffixes() {
	for _, test := range cases {
		for _, ext := range metaSuffixes {
			suite.Run(fmt.Sprintf("%s %s %s", test.service, test.category, ext), func() {
				t := suite.T()

				p, err := path.Build(
					tenant,
					user,
					test.service,
					test.category,
					true,
					"file"+ext)
				require.NoError(t, err, clues.ToCore(err))

				test.expected(t, metadata.IsMetadataFile(p), "extension %s", ext)
			})
		}
	}
}

func (suite *MetadataUnitSuite) TestIsMetadataFile_Files_NotMetaSuffixes() {
	for _, test := range cases {
		for _, ext := range notMetaSuffixes {
			suite.Run(fmt.Sprintf("%s %s %s", test.service, test.category, ext), func() {
				t := suite.T()

				p, err := path.Build(
					tenant,
					user,
					test.service,
					test.category,
					true,
					"file"+ext)
				require.NoError(t, err, clues.ToCore(err))

				assert.Falsef(t, metadata.IsMetadataFile(p), "extension %s", ext)
			})
		}
	}
}

func (suite *MetadataUnitSuite) TestIsMetadataFile_Directories() {
	suffixes := append(append([]string{}, notMetaSuffixes...), metaSuffixes...)

	for _, test := range cases {
		for _, ext := range suffixes {
			suite.Run(fmt.Sprintf("%s %s %s", test.service, test.category, ext), func() {
				t := suite.T()

				p, err := path.Build(
					tenant,
					user,
					test.service,
					test.category,
					false,
					"file"+ext)
				require.NoError(t, err, clues.ToCore(err))

				assert.Falsef(t, metadata.IsMetadataFile(p), "extension %s", ext)
			})
		}
	}
}
