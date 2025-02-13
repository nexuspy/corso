package api_test

import (
	"testing"

	"github.com/alcionai/clues"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/alcionai/corso/src/internal/m365/graph"
	"github.com/alcionai/corso/src/internal/tester"
	"github.com/alcionai/corso/src/pkg/services/m365/api"
)

type UsersUnitSuite struct {
	tester.Suite
}

func TestUsersUnitSuite(t *testing.T) {
	suite.Run(t, &UsersUnitSuite{Suite: tester.NewUnitSuite(t)})
}

func (suite *UsersUnitSuite) TestValidateUser() {
	name := "testuser"
	email := "testuser@foo.com"
	id := "testID"
	user := models.NewUser()
	user.SetUserPrincipalName(&email)
	user.SetDisplayName(&name)
	user.SetId(&id)

	tests := []struct {
		name     string
		args     models.Userable
		errCheck assert.ErrorAssertionFunc
	}{
		{
			name:     "No ID",
			args:     models.NewUser(),
			errCheck: assert.Error,
		},
		{
			name: "No user principal name",
			args: func() *models.User {
				u := models.NewUser()
				u.SetId(&id)
				return u
			}(),
			errCheck: assert.Error,
		},
		{
			name:     "Valid User",
			args:     user,
			errCheck: assert.NoError,
		},
	}
	for _, tt := range tests {
		suite.Run(tt.name, func() {
			t := suite.T()

			err := api.ValidateUser(tt.args)
			tt.errCheck(t, err, clues.ToCore(err))
		})
	}
}

func (suite *UsersUnitSuite) TestEvaluateMailboxError() {
	table := []struct {
		name   string
		err    error
		expect func(t *testing.T, err error)
	}{
		{
			name: "nil",
			err:  nil,
			expect: func(t *testing.T, err error) {
				assert.NoError(t, err, clues.ToCore(err))
			},
		},
		{
			name: "mail inbox err - user not found",
			err:  odErr(string(graph.RequestResourceNotFound)),
			expect: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, graph.ErrResourceOwnerNotFound, clues.ToCore(err))
			},
		},
		{
			name: "mail inbox err - user not found",
			err:  odErr(string(graph.MailboxNotEnabledForRESTAPI)),
			expect: func(t *testing.T, err error) {
				assert.NoError(t, err, clues.ToCore(err))
			},
		},
		{
			name: "mail inbox err - authenticationError",
			err:  odErr(string(graph.AuthenticationError)),
			expect: func(t *testing.T, err error) {
				assert.NoError(t, err, clues.ToCore(err))
			},
		},
		{
			name: "mail inbox err - other error",
			err:  odErrMsg("somecode", "somemessage"),
			expect: func(t *testing.T, err error) {
				assert.Error(t, err, clues.ToCore(err))
			},
		},
	}
	for _, test := range table {
		suite.Run(test.name, func() {
			test.expect(suite.T(), api.EvaluateMailboxError(test.err))
		})
	}
}

func (suite *UsersUnitSuite) TestIsAnyErrMailboxNotFound() {
	table := []struct {
		name   string
		errs   []error
		expect bool
	}{
		{
			name:   "no errors",
			errs:   nil,
			expect: false,
		},
		{
			name: "mailbox not found error",
			errs: []error{
				clues.New("an error"),
				api.ErrMailBoxNotFound,
				clues.New("an error"),
			},
			expect: true,
		},
		{
			name: "other errors",
			errs: []error{
				clues.New("an error"),
				api.ErrMailBoxSettingsAccessDenied,
				clues.New("an error"),
			},
			expect: false,
		},
	}
	for _, test := range table {
		suite.Run(test.name, func() {
			assert.Equal(suite.T(), test.expect, api.IsAnyErrMailboxNotFound(test.errs))
		})
	}
}
