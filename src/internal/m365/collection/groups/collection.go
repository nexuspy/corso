package groups

import (
	"bytes"
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alcionai/clues"
	kjson "github.com/microsoft/kiota-serialization-json-go"

	"github.com/alcionai/corso/src/internal/common/ptr"
	"github.com/alcionai/corso/src/internal/data"
	"github.com/alcionai/corso/src/internal/m365/support"
	"github.com/alcionai/corso/src/internal/observe"
	"github.com/alcionai/corso/src/pkg/backup/details"
	"github.com/alcionai/corso/src/pkg/control"
	"github.com/alcionai/corso/src/pkg/fault"
	"github.com/alcionai/corso/src/pkg/logger"
	"github.com/alcionai/corso/src/pkg/path"
)

var (
	_ data.BackupCollection = &Collection{}
	_ data.Item             = &Item{}
	_ data.ItemInfo         = &Item{}
	_ data.ItemModTime      = &Item{}
)

const (
	collectionChannelBufferSize = 1000
	numberOfRetries             = 4
)

type Collection struct {
	protectedResource string
	stream            chan data.Item

	// added is a list of existing item IDs that were added to a container
	added map[string]struct{}
	// removed is a list of item IDs that were deleted from, or moved out, of a container
	removed map[string]struct{}

	getter getChannelMessager

	category      path.CategoryType
	statusUpdater support.StatusUpdater
	ctrl          control.Options

	// FullPath is the current hierarchical path used by this collection.
	fullPath path.Path

	// PrevPath is the previous hierarchical path used by this collection.
	// It may be the same as fullPath, if the folder was not renamed or
	// moved.  It will be empty on its first retrieval.
	prevPath path.Path

	// LocationPath contains the path with human-readable display names.
	// IE: "/Inbox/Important" instead of "/abcdxyz123/algha=lgkhal=t"
	locationPath *path.Builder

	state data.CollectionState

	// doNotMergeItems should only be true if the old delta token expired.
	doNotMergeItems bool
}

// NewExchangeDataCollection creates an ExchangeDataCollection.
// State of the collection is set as an observation of the current
// and previous paths.  If the curr path is nil, the state is assumed
// to be deleted.  If the prev path is nil, it is assumed newly created.
// If both are populated, then state is either moved (if they differ),
// or notMoved (if they match).
func NewCollection(
	getter getChannelMessager,
	protectedResource string,
	curr, prev path.Path,
	location *path.Builder,
	category path.CategoryType,
	added map[string]struct{},
	removed map[string]struct{},
	statusUpdater support.StatusUpdater,
	ctrlOpts control.Options,
	doNotMergeItems bool,
) Collection {
	collection := Collection{
		added:             added,
		category:          category,
		ctrl:              ctrlOpts,
		doNotMergeItems:   doNotMergeItems,
		fullPath:          curr,
		getter:            getter,
		locationPath:      location,
		prevPath:          prev,
		removed:           removed,
		state:             data.StateOf(prev, curr),
		statusUpdater:     statusUpdater,
		stream:            make(chan data.Item, collectionChannelBufferSize),
		protectedResource: protectedResource,
	}

	return collection
}

// Items utility function to asynchronously execute process to fill data channel with
// M365 exchange objects and returns the data channel
func (col *Collection) Items(ctx context.Context, errs *fault.Bus) <-chan data.Item {
	go col.streamItems(ctx, errs)
	return col.stream
}

// FullPath returns the Collection's fullPath []string
func (col *Collection) FullPath() path.Path {
	return col.fullPath
}

// LocationPath produces the Collection's full path, but with display names
// instead of IDs in the folders.  Only populated for Calendars.
func (col *Collection) LocationPath() *path.Builder {
	return col.locationPath
}

// TODO(ashmrtn): Fill in with previous path once the Controller compares old
// and new folder hierarchies.
func (col Collection) PreviousPath() path.Path {
	return col.prevPath
}

func (col Collection) State() data.CollectionState {
	return col.state
}

func (col Collection) DoNotMergeItems() bool {
	return col.doNotMergeItems
}

// ---------------------------------------------------------------------------
// items
// ---------------------------------------------------------------------------

// Item represents a single item retrieved from exchange
type Item struct {
	id      string
	message []byte
	info    *details.GroupsInfo
	// TODO(ashmrtn): Can probably eventually be sourced from info as there's a
	// request to provide modtime in ItemInfo structs.
	modTime time.Time

	// true if the item was marked by graph as deleted.
	deleted bool
}

func (i *Item) ID() string {
	return i.id
}

func (i *Item) ToReader() io.ReadCloser {
	return io.NopCloser(bytes.NewReader(i.message))
}

func (i Item) Deleted() bool {
	return i.deleted
}

func (i *Item) Info() (details.ItemInfo, error) {
	return details.ItemInfo{Groups: i.info}, nil
}

func (i *Item) ModTime() time.Time {
	return i.modTime
}

func NewItem(
	identifier string,
	dataBytes []byte,
	detail details.GroupsInfo,
	modTime time.Time,
) Item {
	return Item{
		id:      identifier,
		message: dataBytes,
		info:    &detail,
		modTime: modTime,
	}
}

// ---------------------------------------------------------------------------
// items() production
// ---------------------------------------------------------------------------

func (col *Collection) streamItems(ctx context.Context, errs *fault.Bus) {
	var (
		streamedItems int64
		totalBytes    int64
		wg            sync.WaitGroup
		colProgress   chan<- struct{}
		el            = errs.Local()
	)

	ctx = clues.Add(ctx, "category", col.category.String())

	defer func() {
		col.finishPopulation(ctx, streamedItems, totalBytes, errs.Failure())
	}()

	if len(col.added)+len(col.removed) > 0 {
		colProgress = observe.CollectionProgress(
			ctx,
			col.FullPath().Category().HumanString(),
			col.LocationPath().Elements())
		defer close(colProgress)
	}

	semaphoreCh := make(chan struct{}, col.ctrl.Parallelism.ItemFetch)
	defer close(semaphoreCh)

	// delete all removed items
	for id := range col.removed {
		semaphoreCh <- struct{}{}

		wg.Add(1)

		go func(id string) {
			defer wg.Done()
			defer func() { <-semaphoreCh }()

			col.stream <- &Item{
				id:      id,
				modTime: time.Now().UTC(), // removed items have no modTime entry.
				deleted: true,
			}

			atomic.AddInt64(&streamedItems, 1)
			atomic.AddInt64(&totalBytes, 0)

			if colProgress != nil {
				colProgress <- struct{}{}
			}
		}(id)
	}

	// add any new items
	for id := range col.added {
		if el.Failure() != nil {
			break
		}

		wg.Add(1)
		semaphoreCh <- struct{}{}

		go func(id string) {
			defer wg.Done()
			defer func() { <-semaphoreCh }()

			writer := kjson.NewJsonSerializationWriter()
			defer writer.Close()

			flds := col.fullPath.Folders()
			parentFolderID := flds[len(flds)-1]

			item, info, err := col.getter.GetChannelMessage(
				ctx,
				col.protectedResource,
				parentFolderID,
				id)
			if err != nil {
				logger.CtxErr(ctx, err).Info("writing channel message to serializer")
				return
			}

			if err := writer.WriteObjectValue("", item); err != nil {
				logger.CtxErr(ctx, err).Info("writing channel message to serializer")
				return
			}

			data, err := writer.GetSerializedContent()
			if err != nil {
				logger.CtxErr(ctx, err).Info("serializing channel message")
				return
			}

			info.ParentPath = col.LocationPath().String()

			col.stream <- &Item{
				id:      ptr.Val(item.GetId()),
				message: data,
				info:    info,
				modTime: info.Modified,
			}

			atomic.AddInt64(&streamedItems, 1)
			atomic.AddInt64(&totalBytes, info.Size)

			if colProgress != nil {
				colProgress <- struct{}{}
			}
		}(id)
	}

	wg.Wait()
}

// finishPopulation is a utility function used to close a Collection's data channel
// and to send the status update through the channel.
func (col *Collection) finishPopulation(
	ctx context.Context,
	streamedItems, totalBytes int64,
	err error,
) {
	close(col.stream)

	attempted := len(col.added) + len(col.removed)
	status := support.CreateStatus(
		ctx,
		support.Backup,
		1,
		support.CollectionMetrics{
			Objects:   attempted,
			Successes: int(streamedItems),
			Bytes:     totalBytes,
		},
		col.FullPath().Folder(false))

	logger.Ctx(ctx).Debugw("done streaming items", "status", status.String())

	col.statusUpdater(status)
}
