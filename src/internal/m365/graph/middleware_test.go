package graph

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"syscall"
	"testing"
	"time"

	"github.com/alcionai/clues"
	"github.com/google/uuid"
	khttp "github.com/microsoft/kiota-http-go"
	msgraphsdkgo "github.com/microsoftgraph/msgraph-sdk-go"
	msgraphgocore "github.com/microsoftgraph/msgraph-sdk-go-core"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/microsoftgraph/msgraph-sdk-go/users"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"golang.org/x/time/rate"

	"github.com/alcionai/corso/src/internal/common/ptr"
	"github.com/alcionai/corso/src/internal/tester"
	"github.com/alcionai/corso/src/internal/tester/tconfig"
	"github.com/alcionai/corso/src/pkg/account"
	"github.com/alcionai/corso/src/pkg/path"
)

type mwReturns struct {
	err  error
	resp *http.Response
}

func newMWReturns(code int, body []byte, err error) mwReturns {
	var brc io.ReadCloser

	if len(body) > 0 {
		brc = io.NopCloser(bytes.NewBuffer(body))
	}

	resp := &http.Response{
		StatusCode: code,
		Body:       brc,
	}

	if code == 0 {
		resp = nil
	}

	return mwReturns{
		err:  err,
		resp: resp,
	}
}

func newTestMW(onIntercept func(*http.Request), mrs ...mwReturns) *testMW {
	return &testMW{
		onIntercept: onIntercept,
		toReturn:    mrs,
	}
}

type testMW struct {
	repeatReturn0 bool
	iter          int
	toReturn      []mwReturns
	onIntercept   func(*http.Request)
}

func (mw *testMW) Intercept(
	pipeline khttp.Pipeline,
	middlewareIndex int,
	req *http.Request,
) (*http.Response, error) {
	mw.onIntercept(req)

	i := mw.iter
	if mw.repeatReturn0 {
		i = 0
	}

	if i >= len(mw.toReturn) {
		panic(clues.New("middleware test had more calls than responses"))
	}

	tr := mw.toReturn[i]

	mw.iter++

	return tr.resp, tr.err
}

// can't use graph/mock.CreateAdapter() due to circular references.
func mockAdapter(
	creds account.M365Config,
	mw khttp.Middleware,
	timeout time.Duration,
) (*msgraphsdkgo.GraphRequestAdapter, error) {
	auth, err := GetAuth(
		creds.AzureTenantID,
		creds.AzureClientID,
		creds.AzureClientSecret)
	if err != nil {
		return nil, err
	}

	var (
		clientOptions = msgraphsdkgo.GetDefaultClientOptions()
		cc            = populateConfig(MinimumBackoff(10 * time.Millisecond))
		middlewares   = append(kiotaMiddlewares(&clientOptions, cc), mw)
		httpClient    = msgraphgocore.GetDefaultClient(&clientOptions, middlewares...)
	)

	httpClient.Timeout = timeout

	cc.apply(httpClient)

	return msgraphsdkgo.NewGraphRequestAdapterWithParseNodeFactoryAndSerializationWriterFactoryAndHttpClient(
		auth,
		nil, nil,
		httpClient)
}

type RetryMWIntgSuite struct {
	tester.Suite
	creds account.M365Config
}

// We do end up mocking the actual request, but creating the rest
// similar to E2E suite
func TestRetryMWIntgSuite(t *testing.T) {
	suite.Run(t, &RetryMWIntgSuite{
		Suite: tester.NewIntegrationSuite(
			t,
			[][]string{tconfig.M365AcctCredEnvs}),
	})
}

func (suite *RetryMWIntgSuite) SetupSuite() {
	var (
		a   = tconfig.NewM365Account(suite.T())
		err error
	)

	suite.creds, err = a.M365Config()
	require.NoError(suite.T(), err, clues.ToCore(err))
}

func (suite *RetryMWIntgSuite) TestRetryMiddleware_Intercept_byStatusCode() {
	var (
		uri     = "https://graph.microsoft.com"
		urlPath = "/v1.0/users/user/messages/foo"
		url     = uri + urlPath
	)

	tests := []struct {
		name             string
		status           int
		providedErr      error
		expectRetryCount int
		mw               testMW
		expectErr        assert.ErrorAssertionFunc
	}{
		{
			name:             "200, no retries",
			status:           http.StatusOK,
			providedErr:      nil,
			expectRetryCount: 0,
			expectErr:        assert.NoError,
		},
		{
			name:             "400, no retries",
			status:           http.StatusBadRequest,
			providedErr:      nil,
			expectRetryCount: 0,
			expectErr:        assert.Error,
		},
		{
			// don't test 504: gets intercepted by graph client for long waits.
			name:             "502",
			status:           http.StatusBadGateway,
			providedErr:      nil,
			expectRetryCount: defaultMaxRetries,
			expectErr:        assert.Error,
		},
		{
			name:             "conn reset with 5xx",
			status:           http.StatusBadGateway,
			providedErr:      syscall.ECONNRESET,
			expectRetryCount: defaultMaxRetries,
			expectErr:        assert.Error,
		},
		{
			name:             "conn reset with 2xx",
			status:           http.StatusOK,
			providedErr:      syscall.ECONNRESET,
			expectRetryCount: defaultMaxRetries,
			expectErr:        assert.Error,
		},
		{
			name:        "conn reset with nil resp",
			providedErr: syscall.ECONNRESET,
			// Use 0 to denote nil http response
			status:           0,
			expectRetryCount: 3,
			expectErr:        assert.Error,
		},
		{
			// Unlikely but check if connection reset error takes precedence
			name:             "conn reset with 400 resp",
			providedErr:      syscall.ECONNRESET,
			status:           http.StatusBadRequest,
			expectRetryCount: 3,
			expectErr:        assert.Error,
		},
		{
			name:             "http timeout",
			providedErr:      http.ErrHandlerTimeout,
			status:           0,
			expectRetryCount: 3,
			expectErr:        assert.Error,
		},
	}

	for _, test := range tests {
		suite.Run(test.name, func() {
			t := suite.T()

			ctx, flush := tester.NewContext(t)
			defer flush()

			called := 0
			mw := newTestMW(
				func(*http.Request) { called++ },
				newMWReturns(test.status, nil, test.providedErr))
			mw.repeatReturn0 = true

			adpt, err := mockAdapter(suite.creds, mw, 25*time.Second)
			require.NoError(t, err, clues.ToCore(err))

			// url doesn't fit the builder, but that shouldn't matter
			_, err = users.NewCountRequestBuilder(url, adpt).Get(ctx, nil)
			test.expectErr(t, err, clues.ToCore(err))

			// -1 because the non-retried call always counts for one, then
			// we increment based on the number of retry attempts.
			assert.Equal(t, test.expectRetryCount, called-1)
		})
	}
}

func (suite *RetryMWIntgSuite) TestRetryMiddleware_RetryRequest_resetBodyAfter500() {
	t := suite.T()

	ctx, flush := tester.NewContext(t)
	defer flush()

	var (
		body             = models.NewMailFolder()
		checkOnIntercept = func(req *http.Request) {
			bs, err := io.ReadAll(req.Body)
			require.NoError(t, err, clues.ToCore(err))

			// an expired body, after graph compression, will
			// normally contain 25 bytes.  So we should see more
			// than that at least.
			require.Less(
				t,
				25,
				len(bs),
				"body should be longer than 25 bytes; shorter indicates the body was sliced on a retry")
		}
	)

	body.SetDisplayName(ptr.To(uuid.NewString()))

	mw := newTestMW(
		checkOnIntercept,
		newMWReturns(http.StatusInternalServerError, nil, nil),
		newMWReturns(http.StatusOK, nil, nil))

	adpt, err := mockAdapter(suite.creds, mw, 15*time.Second)
	require.NoError(t, err, clues.ToCore(err))

	// no api package needed here, this is a mocked request that works
	// independent of the query.
	_, err = NewService(adpt).
		Client().
		Users().
		ByUserIdString("user").
		MailFolders().
		Post(ctx, body, nil)
	require.NoError(t, err, clues.ToCore(err))
}

func (suite *RetryMWIntgSuite) TestRetryMiddleware_RetryResponse_maintainBodyAfter503() {
	t := suite.T()

	ctx, flush := tester.NewContext(t)
	defer flush()

	InitializeConcurrencyLimiter(ctx, false, -1)

	odem := odErrMsg("SystemDown", "The System, Is Down, bah-dup-da-woo-woo!")
	m := parseableToMap(t, odem)

	body, err := json.Marshal(m)
	require.NoError(t, err, clues.ToCore(err))

	mw := newTestMW(
		// intentional no-op, just need to conrol the response code
		func(*http.Request) {},
		newMWReturns(http.StatusServiceUnavailable, body, nil),
		newMWReturns(http.StatusServiceUnavailable, body, nil),
		newMWReturns(http.StatusServiceUnavailable, body, nil),
		newMWReturns(http.StatusServiceUnavailable, body, nil))

	adpt, err := mockAdapter(suite.creds, mw, 55*time.Second)
	require.NoError(t, err, clues.ToCore(err))

	// no api package needed here,
	// this is a mocked request that works
	// independent of the query.
	_, err = NewService(adpt).
		Client().
		Users().
		ByUserIdString("user").
		MailFolders().
		Post(ctx, models.NewMailFolder(), nil)
	require.Error(t, err, clues.ToCore(err))
	require.NotContains(t, err.Error(), "content is empty", clues.ToCore(err))
	require.Contains(t, err.Error(), "503", clues.ToCore(err))
}

type MiddlewareUnitSuite struct {
	tester.Suite
}

func TestMiddlewareUnitSuite(t *testing.T) {
	suite.Run(t, &MiddlewareUnitSuite{Suite: tester.NewUnitSuite(t)})
}

func (suite *MiddlewareUnitSuite) TestBindExtractLimiterConfig() {
	t := suite.T()

	ctx, flush := tester.NewContext(t)
	defer flush()

	// an unpopulated ctx should produce the default limiter
	assert.Equal(t, defaultLimiter, ctxLimiter(ctx))

	table := []struct {
		name          string
		service       path.ServiceType
		expectOK      require.BoolAssertionFunc
		expectLimiter *rate.Limiter
	}{
		{
			name:          "exchange",
			service:       path.ExchangeService,
			expectLimiter: defaultLimiter,
		},
		{
			name:          "oneDrive",
			service:       path.OneDriveService,
			expectLimiter: driveLimiter,
		},
		{
			name:          "sharePoint",
			service:       path.SharePointService,
			expectLimiter: driveLimiter,
		},
		{
			name:          "groups",
			service:       path.GroupsService,
			expectLimiter: driveLimiter,
		},
		{
			name:          "unknownService",
			service:       path.UnknownService,
			expectLimiter: defaultLimiter,
		},
		{
			name:          "badService",
			service:       path.ServiceType(-1),
			expectLimiter: defaultLimiter,
		},
	}
	for _, test := range table {
		suite.Run(test.name, func() {
			t := suite.T()

			tctx := BindRateLimiterConfig(ctx, LimiterCfg{Service: test.service})
			lc, ok := extractRateLimiterConfig(tctx)
			require.True(t, ok, "found rate limiter in ctx")
			assert.Equal(t, test.service, lc.Service)
			assert.Equal(t, test.expectLimiter, ctxLimiter(tctx))
		})
	}
}

func (suite *MiddlewareUnitSuite) TestLimiterConsumption() {
	t := suite.T()

	ctx, flush := tester.NewContext(t)
	defer flush()

	// an unpopulated ctx should produce the default consumption
	assert.Equal(t, defaultLC, ctxLimiterConsumption(ctx, defaultLC))

	table := []struct {
		name   string
		n      int
		expect int
	}{
		{
			name:   "matches default",
			n:      defaultLC,
			expect: defaultLC,
		},
		{
			name:   "default+1",
			n:      defaultLC + 1,
			expect: defaultLC + 1,
		},
		{
			name:   "zero",
			n:      0,
			expect: defaultLC,
		},
		{
			name:   "negative",
			n:      -1,
			expect: defaultLC,
		},
	}
	for _, test := range table {
		suite.Run(test.name, func() {
			t := suite.T()

			tctx := ConsumeNTokens(ctx, test.n)
			lc := ctxLimiterConsumption(tctx, defaultLC)
			assert.Equal(t, test.expect, lc)
		})
	}
}
