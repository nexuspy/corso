package operations

import (
	"context"
	"fmt"

	"github.com/alcionai/clues"

	"github.com/alcionai/corso/src/pkg/control"
	"github.com/alcionai/corso/src/pkg/fault"
	"github.com/alcionai/corso/src/pkg/logger"
)

// finalizeErrorHandling ensures the operation follows the
// failure policy requirements.
func finalizeErrorHandling(
	ctx context.Context,
	opts control.Options,
	errs *fault.Bus,
	prefix string,
) {
	rcvd := errs.Recovered()

	// under certain conditions, there's nothing else left to do
	if opts.FailureHandling == control.BestEffort ||
		errs.Failure() != nil ||
		len(rcvd) == 0 {
		return
	}

	if opts.FailureHandling == control.FailAfterRecovery {
		msg := fmt.Sprintf("%s: partial success: %d errors occurred", prefix, len(rcvd))
		logger.Ctx(ctx).Error(msg)

		if len(rcvd) == 1 {
			errs.Fail(rcvd[0])
			return
		}

		errs.Fail(clues.New(msg))
	}
}

// LogFaultErrors is a helper function that logs all entries in the Errors struct.
func LogFaultErrors(ctx context.Context, fe *fault.Errors, prefix string) {
	if fe == nil {
		return
	}

	var (
		log        = logger.Ctx(ctx)
		pfxMsg     = prefix + ":"
		li, ls, lr = len(fe.Items), len(fe.Skipped), len(fe.Recovered)
	)

	if fe.Failure == nil && li+ls+lr == 0 {
		log.Info(pfxMsg + " no issues")
		return
	}

	if fe.Failure != nil {
		log.With("error", fe.Failure).Errorf("%s primary failure: %s", pfxMsg, fe.Failure.Msg)
	}

	for i, item := range fe.Items {
		log.With("failed_item", item).Errorf("%s item failure %d of %d: %s", pfxMsg, i+1, li, item.Cause)
	}

	for i, item := range fe.Skipped {
		log.With("skipped_item", item).Errorf("%s skipped item %d of %d: %s", pfxMsg, i+1, ls, item.Item.Cause)
	}

	for i, err := range fe.Recovered {
		log.With("recovered_error", err).Errorf("%s recoverable error %d of %d: %s", pfxMsg, i+1, lr, err.Msg)
	}
}
