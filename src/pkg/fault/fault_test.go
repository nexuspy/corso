package fault_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alcionai/clues"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/alcionai/corso/src/internal/tester"
	"github.com/alcionai/corso/src/pkg/fault"
)

type FaultErrorsUnitSuite struct {
	tester.Suite
}

func TestFaultErrorsUnitSuite(t *testing.T) {
	suite.Run(t, &FaultErrorsUnitSuite{Suite: tester.NewUnitSuite(t)})
}

func (suite *FaultErrorsUnitSuite) TestNew() {
	t := suite.T()

	n := fault.New(false)
	assert.NotNil(t, n)

	n = fault.New(true)
	assert.NotNil(t, n)
}

func (suite *FaultErrorsUnitSuite) TestErr() {
	table := []struct {
		name     string
		failFast bool
		fail     error
		add      error
		expect   assert.ErrorAssertionFunc
	}{
		{
			name:   "nil",
			expect: assert.NoError,
		},
		{
			name:     "nil, failFast",
			failFast: true,
			expect:   assert.NoError,
		},
		{
			name:   "failed",
			fail:   assert.AnError,
			expect: assert.Error,
		},
		{
			name:     "failed, failFast",
			fail:     assert.AnError,
			failFast: true,
			expect:   assert.Error,
		},
		{
			name:   "added",
			add:    assert.AnError,
			expect: assert.NoError,
		},
		{
			name:     "added, failFast",
			add:      assert.AnError,
			failFast: true,
			expect:   assert.Error,
		},
	}
	for _, test := range table {
		suite.Run(test.name, func() {
			t := suite.T()

			ctx, flush := tester.NewContext(t)
			defer flush()

			n := fault.New(test.failFast)
			require.NotNil(t, n)
			require.NoError(t, n.Failure(), clues.ToCore(n.Failure()))
			require.Empty(t, n.Recovered())

			e := n.Fail(test.fail)
			require.NotNil(t, e)

			n.AddRecoverable(ctx, test.add)

			test.expect(t, n.Failure())
		})
	}
}

func (suite *FaultErrorsUnitSuite) TestFail() {
	t := suite.T()

	n := fault.New(false)
	require.NotNil(t, n)
	require.NoError(t, n.Failure(), clues.ToCore(n.Failure()))
	require.Empty(t, n.Recovered())

	n.Fail(assert.AnError)
	assert.Error(t, n.Failure(), clues.ToCore(n.Failure()))
	assert.Empty(t, n.Recovered())

	n.Fail(assert.AnError)
	assert.Error(t, n.Failure())
	assert.NotEmpty(t, n.Recovered())
}

func (suite *FaultErrorsUnitSuite) TestErrs() {
	table := []struct {
		name     string
		failFast bool
		fail     error
		add      error
		expect   assert.ValueAssertionFunc
	}{
		{
			name:   "nil",
			expect: assert.Empty,
		},
		{
			name:     "nil, failFast",
			failFast: true,
			expect:   assert.Empty,
		},
		{
			name:   "failed",
			fail:   assert.AnError,
			expect: assert.Empty,
		},
		{
			name:     "failed, failFast",
			fail:     assert.AnError,
			failFast: true,
			expect:   assert.Empty,
		},
		{
			name:   "added",
			add:    assert.AnError,
			expect: assert.NotEmpty,
		},
		{
			name:     "added, failFast",
			add:      assert.AnError,
			failFast: true,
			expect:   assert.NotEmpty,
		},
	}
	for _, test := range table {
		suite.Run(test.name, func() {
			t := suite.T()

			ctx, flush := tester.NewContext(t)
			defer flush()

			n := fault.New(test.failFast)
			require.NotNil(t, n)

			e := n.Fail(test.fail)
			require.NotNil(t, e)

			n.AddRecoverable(ctx, test.add)

			test.expect(t, n.Recovered())
		})
	}
}

func (suite *FaultErrorsUnitSuite) TestAdd() {
	t := suite.T()

	ctx, flush := tester.NewContext(t)
	defer flush()

	n := fault.New(true)
	require.NotNil(t, n)

	n.AddRecoverable(ctx, assert.AnError)
	assert.Error(t, n.Failure())
	assert.Len(t, n.Recovered(), 1)

	n.AddRecoverable(ctx, assert.AnError)
	assert.Error(t, n.Failure())
	assert.Len(t, n.Recovered(), 2)
}

func (suite *FaultErrorsUnitSuite) TestAddSkip() {
	t := suite.T()

	ctx, flush := tester.NewContext(t)
	defer flush()

	n := fault.New(true)
	require.NotNil(t, n)

	n.Fail(assert.AnError)
	assert.Len(t, n.Skipped(), 0)

	n.AddRecoverable(ctx, assert.AnError)
	assert.Len(t, n.Skipped(), 0)

	n.AddSkip(ctx, fault.OwnerSkip(fault.SkipMalware, "ns", "id", "name", nil))
	assert.Len(t, n.Skipped(), 1)
}

func (suite *FaultErrorsUnitSuite) TestErrors() {
	t := suite.T()

	ctx, flush := tester.NewContext(t)
	defer flush()

	// not fail-fast
	n := fault.New(false)
	require.NotNil(t, n)

	n.Fail(clues.New("fail"))
	n.AddRecoverable(ctx, clues.New("1"))
	n.AddRecoverable(ctx, clues.New("2"))

	d := n.Errors()
	assert.Equal(t, clues.ToCore(n.Failure()), d.Failure)
	assert.Len(t, d.Recovered, len(n.Recovered()))
	assert.False(t, d.FailFast)

	// fail-fast
	n = fault.New(true)
	require.NotNil(t, n)

	n.Fail(clues.New("fail"))
	n.AddRecoverable(ctx, clues.New("1"))
	n.AddRecoverable(ctx, clues.New("2"))

	d = n.Errors()
	assert.Equal(t, clues.ToCore(n.Failure()), d.Failure)
	assert.Len(t, d.Recovered, len(n.Recovered()))
	assert.True(t, d.FailFast)
}

func (suite *FaultErrorsUnitSuite) TestErrors_Items() {
	ae := clues.Stack(assert.AnError)
	noncore := []*clues.ErrCore{ae.Core()}
	addtl := map[string]any{"foo": "bar", "baz": 1}

	table := []struct {
		name              string
		errs              func(context.Context) *fault.Errors
		expectItems       []fault.Item
		expectRecoverable []*clues.ErrCore
	}{
		{
			name: "no errors",
			errs: func(ctx context.Context) *fault.Errors {
				return fault.New(false).Errors()
			},
			expectItems:       []fault.Item{},
			expectRecoverable: []*clues.ErrCore{},
		},
		{
			name: "no items",
			errs: func(ctx context.Context) *fault.Errors {
				b := fault.New(false)
				b.Fail(ae)
				b.AddRecoverable(ctx, ae)

				return b.Errors()
			},
			expectItems:       []fault.Item{},
			expectRecoverable: noncore,
		},
		{
			name: "failure item",
			errs: func(ctx context.Context) *fault.Errors {
				b := fault.New(false)
				b.Fail(fault.OwnerErr(ae, "ns", "id", "name", addtl))
				b.AddRecoverable(ctx, ae)

				return b.Errors()
			},
			expectItems:       []fault.Item{*fault.OwnerErr(ae, "ns", "id", "name", addtl)},
			expectRecoverable: noncore,
		},
		{
			name: "recoverable item",
			errs: func(ctx context.Context) *fault.Errors {
				b := fault.New(false)
				b.Fail(ae)
				b.AddRecoverable(ctx, fault.OwnerErr(ae, "ns", "id", "name", addtl))

				return b.Errors()
			},
			expectItems:       []fault.Item{*fault.OwnerErr(ae, "ns", "id", "name", addtl)},
			expectRecoverable: []*clues.ErrCore{},
		},
		{
			name: "two items",
			errs: func(ctx context.Context) *fault.Errors {
				b := fault.New(false)
				b.Fail(fault.OwnerErr(ae, "ns", "oid", "name", addtl))
				b.AddRecoverable(ctx, fault.FileErr(ae, "ns", "fid", "name", addtl))

				return b.Errors()
			},
			expectItems: []fault.Item{
				*fault.OwnerErr(ae, "ns", "oid", "name", addtl),
				*fault.FileErr(ae, "ns", "fid", "name", addtl),
			},
			expectRecoverable: []*clues.ErrCore{},
		},
		{
			name: "two items - diff namespace same id",
			errs: func(ctx context.Context) *fault.Errors {
				b := fault.New(false)
				b.Fail(fault.OwnerErr(ae, "ns", "id", "name", addtl))
				b.AddRecoverable(ctx, fault.FileErr(ae, "ns2", "id", "name", addtl))

				return b.Errors()
			},
			expectItems: []fault.Item{
				*fault.OwnerErr(ae, "ns", "id", "name", addtl),
				*fault.FileErr(ae, "ns2", "id", "name", addtl),
			},
			expectRecoverable: []*clues.ErrCore{},
		},
		{
			name: "duplicate items - failure priority",
			errs: func(ctx context.Context) *fault.Errors {
				b := fault.New(false)
				b.Fail(fault.OwnerErr(ae, "ns", "id", "name", addtl))
				b.AddRecoverable(ctx, fault.FileErr(ae, "ns", "id", "name", addtl))

				return b.Errors()
			},
			expectItems: []fault.Item{
				*fault.OwnerErr(ae, "ns", "id", "name", addtl),
			},
			expectRecoverable: []*clues.ErrCore{},
		},
		{
			name: "duplicate items - last recoverable priority",
			errs: func(ctx context.Context) *fault.Errors {
				b := fault.New(false)
				b.Fail(ae)
				b.AddRecoverable(ctx, fault.FileErr(ae, "ns", "fid", "name", addtl))
				b.AddRecoverable(ctx, fault.FileErr(ae, "ns", "fid", "name2", addtl))

				return b.Errors()
			},
			expectItems: []fault.Item{
				*fault.FileErr(ae, "ns", "fid", "name2", addtl),
			},
			expectRecoverable: []*clues.ErrCore{},
		},
		{
			name: "recoverable item and non-items",
			errs: func(ctx context.Context) *fault.Errors {
				b := fault.New(false)
				b.Fail(ae)
				b.AddRecoverable(ctx, fault.FileErr(ae, "ns", "fid", "name", addtl))
				b.AddRecoverable(ctx, ae)

				return b.Errors()
			},
			expectItems: []fault.Item{
				*fault.FileErr(ae, "ns", "fid", "name", addtl),
			},
			expectRecoverable: noncore,
		},
	}
	for _, test := range table {
		suite.Run(test.name, func() {
			t := suite.T()

			ctx, flush := tester.NewContext(t)
			defer flush()

			fe := test.errs(ctx)

			assert.ElementsMatch(t, test.expectItems, fe.Items)
			require.Equal(t, test.expectRecoverable, fe.Recovered)

			for i := range test.expectRecoverable {
				expect := test.expectRecoverable[i]
				got := fe.Recovered[i]

				assert.Equal(t, *expect, *got)
			}
		})
	}
}

func (suite *FaultErrorsUnitSuite) TestMarshalUnmarshal() {
	t := suite.T()

	ctx, flush := tester.NewContext(t)
	defer flush()

	// not fail-fast
	n := fault.New(false)
	require.NotNil(t, n)

	n.AddRecoverable(ctx, clues.New("1"))
	n.AddRecoverable(ctx, clues.New("2"))

	bs, err := json.Marshal(n.Errors())
	require.NoError(t, err, clues.ToCore(err))

	err = json.Unmarshal(bs, &fault.Errors{})
	require.NoError(t, err, clues.ToCore(err))
}

type legacyErrorsData struct {
	Err      error   `json:"err"`
	Errs     []error `json:"errs"`
	FailFast bool    `json:"failFast"`
}

func (suite *FaultErrorsUnitSuite) TestUnmarshalLegacy() {
	t := suite.T()

	oldData := &legacyErrorsData{
		Errs: []error{clues.New("foo error"), clues.New("foo error"), clues.New("foo error")},
	}

	jsonStr, err := json.Marshal(oldData)
	require.NoError(t, err, clues.ToCore(err))

	t.Logf("jsonStr is %s\n", jsonStr)

	um := fault.Errors{}

	err = json.Unmarshal(jsonStr, &um)
	require.NoError(t, err, clues.ToCore(err))
}

func (suite *FaultErrorsUnitSuite) TestTracker() {
	t := suite.T()

	ctx, flush := tester.NewContext(t)
	defer flush()

	eb := fault.New(false)

	lb := eb.Local()
	assert.NoError(t, lb.Failure(), clues.ToCore(lb.Failure()))
	assert.Empty(t, eb.Recovered())

	lb.AddRecoverable(ctx, assert.AnError)
	assert.NoError(t, lb.Failure(), clues.ToCore(lb.Failure()))
	assert.NoError(t, eb.Failure(), clues.ToCore(eb.Failure()))
	assert.NotEmpty(t, eb.Recovered())

	ebt := fault.New(true)

	lbt := ebt.Local()
	assert.NoError(t, lbt.Failure(), clues.ToCore(lbt.Failure()))
	assert.Empty(t, ebt.Recovered())

	lbt.AddRecoverable(ctx, assert.AnError)
	assert.Error(t, lbt.Failure())
	assert.Error(t, ebt.Failure())
	assert.NotEmpty(t, ebt.Recovered())
}
