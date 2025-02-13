package repository

import (
	"context"
	"time"

	"github.com/alcionai/clues"
	"github.com/google/uuid"
	"github.com/kopia/kopia/repo/manifest"
	"github.com/pkg/errors"

	"github.com/alcionai/corso/src/internal/common/crash"
	"github.com/alcionai/corso/src/internal/common/idname"
	"github.com/alcionai/corso/src/internal/data"
	"github.com/alcionai/corso/src/internal/events"
	"github.com/alcionai/corso/src/internal/kopia"
	"github.com/alcionai/corso/src/internal/m365"
	"github.com/alcionai/corso/src/internal/m365/collection/drive/metadata"
	"github.com/alcionai/corso/src/internal/model"
	"github.com/alcionai/corso/src/internal/observe"
	"github.com/alcionai/corso/src/internal/operations"
	"github.com/alcionai/corso/src/internal/streamstore"
	"github.com/alcionai/corso/src/internal/version"
	"github.com/alcionai/corso/src/pkg/account"
	"github.com/alcionai/corso/src/pkg/backup"
	"github.com/alcionai/corso/src/pkg/backup/details"
	"github.com/alcionai/corso/src/pkg/control"
	ctrlRepo "github.com/alcionai/corso/src/pkg/control/repository"
	"github.com/alcionai/corso/src/pkg/count"
	"github.com/alcionai/corso/src/pkg/fault"
	"github.com/alcionai/corso/src/pkg/logger"
	"github.com/alcionai/corso/src/pkg/path"
	"github.com/alcionai/corso/src/pkg/selectors"
	"github.com/alcionai/corso/src/pkg/storage"
	"github.com/alcionai/corso/src/pkg/store"
)

var (
	ErrorRepoAlreadyExists = clues.New("a repository was already initialized with that configuration")
	ErrorBackupNotFound    = clues.New("no backup exists with that id")
)

// BackupGetter deals with retrieving metadata about backups from the
// repository.
type BackupGetter interface {
	Backup(ctx context.Context, id string) (*backup.Backup, error)
	Backups(ctx context.Context, ids []string) ([]*backup.Backup, *fault.Bus)
	BackupsByTag(ctx context.Context, fs ...store.FilterOption) ([]*backup.Backup, error)
	GetBackupDetails(
		ctx context.Context,
		backupID string,
	) (*details.Details, *backup.Backup, *fault.Bus)
	GetBackupErrors(
		ctx context.Context,
		backupID string,
	) (*fault.Errors, *backup.Backup, *fault.Bus)
}

type Repository interface {
	GetID() string
	Close(context.Context) error
	NewBackup(
		ctx context.Context,
		self selectors.Selector,
	) (operations.BackupOperation, error)
	NewBackupWithLookup(
		ctx context.Context,
		self selectors.Selector,
		ins idname.Cacher,
	) (operations.BackupOperation, error)
	NewRestore(
		ctx context.Context,
		backupID string,
		sel selectors.Selector,
		restoreCfg control.RestoreConfig,
	) (operations.RestoreOperation, error)
	NewExport(
		ctx context.Context,
		backupID string,
		sel selectors.Selector,
		exportCfg control.ExportConfig,
	) (operations.ExportOperation, error)
	NewMaintenance(
		ctx context.Context,
		mOpts ctrlRepo.Maintenance,
	) (operations.MaintenanceOperation, error)
	NewRetentionConfig(
		ctx context.Context,
		rcOpts ctrlRepo.Retention,
	) (operations.RetentionConfigOperation, error)
	DeleteBackups(ctx context.Context, failOnMissing bool, ids ...string) error
	BackupGetter
	// ConnectToM365 establishes graph api connections
	// and initializes api client configurations.
	ConnectToM365(
		ctx context.Context,
		pst path.ServiceType,
	) (*m365.Controller, error)
}

// Repository contains storage provider information.
type repository struct {
	ID        string
	CreatedAt time.Time
	Version   string // in case of future breaking changes

	Account account.Account // the user's m365 account connection details
	Storage storage.Storage // the storage provider details and configuration
	Opts    control.Options

	Bus        events.Eventer
	dataLayer  *kopia.Wrapper
	modelStore *kopia.ModelStore
}

func (r repository) GetID() string {
	return r.ID
}

// Initialize will:
//   - validate the m365 account & secrets
//   - connect to the m365 account to ensure communication capability
//   - validate the provider config & secrets
//   - initialize the kopia repo with the provider and retention parameters
//   - update maintenance retention parameters as needed
//   - store the configuration details
//   - connect to the provider
//   - return the connected repository
func Initialize(
	ctx context.Context,
	acct account.Account,
	s storage.Storage,
	opts control.Options,
	retentionOpts ctrlRepo.Retention,
) (repo Repository, err error) {
	ctx = clues.Add(
		ctx,
		"acct_provider", acct.Provider.String(),
		"acct_id", clues.Hide(acct.ID()),
		"storage_provider", s.Provider.String())

	defer func() {
		if crErr := crash.Recovery(ctx, recover(), "repo init"); crErr != nil {
			err = crErr
		}
	}()

	kopiaRef := kopia.NewConn(s)
	if err := kopiaRef.Initialize(ctx, opts.Repo, retentionOpts); err != nil {
		// replace common internal errors so that sdk users can check results with errors.Is()
		if errors.Is(err, kopia.ErrorRepoAlreadyExists) {
			return nil, clues.Stack(ErrorRepoAlreadyExists, err).WithClues(ctx)
		}

		return nil, clues.Wrap(err, "initializing kopia")
	}
	// kopiaRef comes with a count of 1 and NewWrapper/NewModelStore bumps it again so safe
	// to close here.
	defer kopiaRef.Close(ctx)

	w, err := kopia.NewWrapper(kopiaRef)
	if err != nil {
		return nil, clues.Stack(err).WithClues(ctx)
	}

	ms, err := kopia.NewModelStore(kopiaRef)
	if err != nil {
		return nil, clues.Stack(err).WithClues(ctx)
	}

	bus, err := events.NewBus(ctx, s, acct.ID(), opts)
	if err != nil {
		return nil, clues.Wrap(err, "constructing event bus")
	}

	repoID := newRepoID(s)
	bus.SetRepoID(repoID)

	r := &repository{
		ID:         repoID,
		Version:    "v1",
		Account:    acct,
		Storage:    s,
		Bus:        bus,
		Opts:       opts,
		dataLayer:  w,
		modelStore: ms,
	}

	if err := newRepoModel(ctx, ms, r.ID); err != nil {
		return nil, clues.New("setting up repository").WithClues(ctx)
	}

	r.Bus.Event(ctx, events.RepoInit, nil)

	return r, nil
}

// Connect will:
//   - validate the m365 account details
//   - connect to the m365 account to ensure communication capability
//   - connect to the provider storage
//   - return the connected repository
func Connect(
	ctx context.Context,
	acct account.Account,
	s storage.Storage,
	repoid string,
	opts control.Options,
) (r Repository, err error) {
	ctx = clues.Add(
		ctx,
		"acct_provider", acct.Provider.String(),
		"acct_id", clues.Hide(acct.ID()),
		"storage_provider", s.Provider.String())

	defer func() {
		if crErr := crash.Recovery(ctx, recover(), "repo connect"); crErr != nil {
			err = crErr
		}
	}()

	progressBar := observe.MessageWithCompletion(ctx, "Connecting to repository")
	defer close(progressBar)

	kopiaRef := kopia.NewConn(s)
	if err := kopiaRef.Connect(ctx, opts.Repo); err != nil {
		return nil, clues.Wrap(err, "connecting kopia client")
	}
	// kopiaRef comes with a count of 1 and NewWrapper/NewModelStore bumps it again so safe
	// to close here.
	defer kopiaRef.Close(ctx)

	w, err := kopia.NewWrapper(kopiaRef)
	if err != nil {
		return nil, clues.Stack(err).WithClues(ctx)
	}

	ms, err := kopia.NewModelStore(kopiaRef)
	if err != nil {
		return nil, clues.Stack(err).WithClues(ctx)
	}

	bus, err := events.NewBus(ctx, s, acct.ID(), opts)
	if err != nil {
		return nil, clues.Wrap(err, "constructing event bus")
	}

	if repoid == events.RepoIDNotFound {
		rm, err := getRepoModel(ctx, ms)
		if err != nil {
			return nil, clues.New("retrieving repo info")
		}

		repoid = string(rm.ID)
	}

	// Do not query repo ID if metrics are disabled
	if !opts.DisableMetrics {
		bus.SetRepoID(repoid)
	}

	// todo: ID and CreatedAt should get retrieved from a stored kopia config.
	return &repository{
		ID:         repoid,
		Version:    "v1",
		Account:    acct,
		Storage:    s,
		Bus:        bus,
		Opts:       opts,
		dataLayer:  w,
		modelStore: ms,
	}, nil
}

func ConnectAndSendConnectEvent(ctx context.Context,
	acct account.Account,
	s storage.Storage,
	repoid string,
	opts control.Options,
) (Repository, error) {
	repo, err := Connect(ctx, acct, s, repoid, opts)
	if err != nil {
		return nil, err
	}

	r := repo.(*repository)
	r.Bus.Event(ctx, events.RepoConnect, nil)

	return r, nil
}

func (r *repository) Close(ctx context.Context) error {
	if err := r.Bus.Close(); err != nil {
		logger.Ctx(ctx).With("err", err).Debugw("closing the event bus", clues.In(ctx).Slice()...)
	}

	if r.dataLayer != nil {
		if err := r.dataLayer.Close(ctx); err != nil {
			logger.Ctx(ctx).With("err", err).Debugw("closing Datalayer", clues.In(ctx).Slice()...)
		}

		r.dataLayer = nil
	}

	if r.modelStore != nil {
		if err := r.modelStore.Close(ctx); err != nil {
			logger.Ctx(ctx).With("err", err).Debugw("closing modelStore", clues.In(ctx).Slice()...)
		}

		r.modelStore = nil
	}

	return nil
}

// NewBackup generates a BackupOperation runner.
func (r repository) NewBackup(
	ctx context.Context,
	sel selectors.Selector,
) (operations.BackupOperation, error) {
	return r.NewBackupWithLookup(ctx, sel, nil)
}

// NewBackupWithLookup generates a BackupOperation runner.
// ownerIDToName and ownerNameToID are optional populations, in case the caller has
// already generated those values.
func (r repository) NewBackupWithLookup(
	ctx context.Context,
	sel selectors.Selector,
	ins idname.Cacher,
) (operations.BackupOperation, error) {
	ctrl, err := connectToM365(ctx, sel.PathService(), r.Account, r.Opts)
	if err != nil {
		return operations.BackupOperation{}, clues.Wrap(err, "connecting to m365")
	}

	ownerID, ownerName, err := ctrl.PopulateProtectedResourceIDAndName(ctx, sel.DiscreteOwner, ins)
	if err != nil {
		return operations.BackupOperation{}, clues.Wrap(err, "resolving resource owner details")
	}

	// TODO: retrieve display name from gc
	sel = sel.SetDiscreteOwnerIDName(ownerID, ownerName)

	return operations.NewBackupOperation(
		ctx,
		r.Opts,
		r.dataLayer,
		store.NewWrapper(r.modelStore),
		ctrl,
		r.Account,
		sel,
		sel, // the selector acts as an IDNamer for its discrete resource owner.
		r.Bus)
}

// NewExport generates a exportOperation runner.
func (r repository) NewExport(
	ctx context.Context,
	backupID string,
	sel selectors.Selector,
	exportCfg control.ExportConfig,
) (operations.ExportOperation, error) {
	ctrl, err := connectToM365(ctx, sel.PathService(), r.Account, r.Opts)
	if err != nil {
		return operations.ExportOperation{}, clues.Wrap(err, "connecting to m365")
	}

	return operations.NewExportOperation(
		ctx,
		r.Opts,
		r.dataLayer,
		store.NewWrapper(r.modelStore),
		ctrl,
		r.Account,
		model.StableID(backupID),
		sel,
		exportCfg,
		r.Bus)
}

// NewRestore generates a restoreOperation runner.
func (r repository) NewRestore(
	ctx context.Context,
	backupID string,
	sel selectors.Selector,
	restoreCfg control.RestoreConfig,
) (operations.RestoreOperation, error) {
	ctrl, err := connectToM365(ctx, sel.PathService(), r.Account, r.Opts)
	if err != nil {
		return operations.RestoreOperation{}, clues.Wrap(err, "connecting to m365")
	}

	return operations.NewRestoreOperation(
		ctx,
		r.Opts,
		r.dataLayer,
		store.NewWrapper(r.modelStore),
		ctrl,
		r.Account,
		model.StableID(backupID),
		sel,
		restoreCfg,
		r.Bus,
		count.New())
}

func (r repository) NewMaintenance(
	ctx context.Context,
	mOpts ctrlRepo.Maintenance,
) (operations.MaintenanceOperation, error) {
	return operations.NewMaintenanceOperation(
		ctx,
		r.Opts,
		r.dataLayer,
		mOpts,
		r.Bus)
}

func (r repository) NewRetentionConfig(
	ctx context.Context,
	rcOpts ctrlRepo.Retention,
) (operations.RetentionConfigOperation, error) {
	return operations.NewRetentionConfigOperation(
		ctx,
		r.Opts,
		r.dataLayer,
		rcOpts,
		r.Bus)
}

// Backup retrieves a backup by id.
func (r repository) Backup(ctx context.Context, id string) (*backup.Backup, error) {
	return getBackup(ctx, id, store.NewWrapper(r.modelStore))
}

// getBackup handles the processing for Backup.
func getBackup(
	ctx context.Context,
	id string,
	sw store.BackupGetter,
) (*backup.Backup, error) {
	b, err := sw.GetBackup(ctx, model.StableID(id))
	if err != nil {
		return nil, errWrapper(err)
	}

	return b, nil
}

// Backups lists backups by ID. Returns as many backups as possible with
// errors for the backups it was unable to retrieve.
func (r repository) Backups(ctx context.Context, ids []string) ([]*backup.Backup, *fault.Bus) {
	var (
		bups []*backup.Backup
		errs = fault.New(false)
		sw   = store.NewWrapper(r.modelStore)
	)

	for _, id := range ids {
		ictx := clues.Add(ctx, "backup_id", id)

		b, err := sw.GetBackup(ictx, model.StableID(id))
		if err != nil {
			errs.AddRecoverable(ctx, errWrapper(err))
		}

		bups = append(bups, b)
	}

	return bups, errs
}

// BackupsByTag lists all backups in a repository that contain all the tags
// specified.
func (r repository) BackupsByTag(ctx context.Context, fs ...store.FilterOption) ([]*backup.Backup, error) {
	sw := store.NewWrapper(r.modelStore)
	return backupsByTag(ctx, sw, fs)
}

// backupsByTag returns all backups matching all provided tags.
//
// TODO(ashmrtn): This exists mostly for testing, but we could restructure the
// code in this file so there's a more elegant mocking solution.
func backupsByTag(
	ctx context.Context,
	sw store.BackupWrapper,
	fs []store.FilterOption,
) ([]*backup.Backup, error) {
	bs, err := sw.GetBackups(ctx, fs...)
	if err != nil {
		return nil, clues.Stack(err)
	}

	// Filter out assist backup bases as they're considered incomplete and we
	// haven't been displaying them before now.
	res := make([]*backup.Backup, 0, len(bs))

	for _, b := range bs {
		if t := b.Tags[model.BackupTypeTag]; t != model.AssistBackup {
			res = append(res, b)
		}
	}

	return res, nil
}

// BackupDetails returns the specified backup.Details
func (r repository) GetBackupDetails(
	ctx context.Context,
	backupID string,
) (*details.Details, *backup.Backup, *fault.Bus) {
	errs := fault.New(false)

	deets, bup, err := getBackupDetails(
		ctx,
		backupID,
		r.Account.ID(),
		r.dataLayer,
		store.NewWrapper(r.modelStore),
		errs)

	return deets, bup, errs.Fail(err)
}

// getBackupDetails handles the processing for GetBackupDetails.
func getBackupDetails(
	ctx context.Context,
	backupID, tenantID string,
	kw *kopia.Wrapper,
	sw store.BackupGetter,
	errs *fault.Bus,
) (*details.Details, *backup.Backup, error) {
	b, err := sw.GetBackup(ctx, model.StableID(backupID))
	if err != nil {
		return nil, nil, errWrapper(err)
	}

	ssid := b.StreamStoreID
	if len(ssid) == 0 {
		ssid = b.DetailsID
	}

	if len(ssid) == 0 {
		return nil, b, clues.New("no streamstore id in backup").WithClues(ctx)
	}

	var (
		sstore = streamstore.NewStreamer(kw, tenantID, b.Selector.PathService())
		deets  details.Details
	)

	err = sstore.Read(
		ctx,
		ssid,
		streamstore.DetailsReader(details.UnmarshalTo(&deets)),
		errs)
	if err != nil {
		return nil, nil, err
	}

	// Retroactively fill in isMeta information for items in older
	// backup versions without that info
	// version.Restore2 introduces the IsMeta flag, so only v1 needs a check.
	if b.Version >= version.OneDrive1DataAndMetaFiles && b.Version < version.OneDrive3IsMetaMarker {
		for _, d := range deets.Entries {
			if d.OneDrive != nil {
				d.OneDrive.IsMeta = metadata.HasMetaSuffix(d.RepoRef)
			}
		}
	}

	deets.DetailsModel = deets.FilterMetaFiles()

	return &deets, b, nil
}

// BackupErrors returns the specified backup's fault.Errors
func (r repository) GetBackupErrors(
	ctx context.Context,
	backupID string,
) (*fault.Errors, *backup.Backup, *fault.Bus) {
	errs := fault.New(false)

	fe, bup, err := getBackupErrors(
		ctx,
		backupID,
		r.Account.ID(),
		r.dataLayer,
		store.NewWrapper(r.modelStore),
		errs)

	return fe, bup, errs.Fail(err)
}

// getBackupErrors handles the processing for GetBackupErrors.
func getBackupErrors(
	ctx context.Context,
	backupID, tenantID string,
	kw *kopia.Wrapper,
	sw store.BackupGetter,
	errs *fault.Bus,
) (*fault.Errors, *backup.Backup, error) {
	b, err := sw.GetBackup(ctx, model.StableID(backupID))
	if err != nil {
		return nil, nil, errWrapper(err)
	}

	ssid := b.StreamStoreID
	if len(ssid) == 0 {
		return nil, b, clues.New("missing streamstore id in backup").WithClues(ctx)
	}

	var (
		sstore = streamstore.NewStreamer(kw, tenantID, b.Selector.PathService())
		fe     fault.Errors
	)

	err = sstore.Read(
		ctx,
		ssid,
		streamstore.FaultErrorsReader(fault.UnmarshalErrorsTo(&fe)),
		errs)
	if err != nil {
		return nil, nil, err
	}

	return &fe, b, nil
}

// DeleteBackups removes the backups from both the model store and the backup
// storage.
//
// If failOnMissing is true then returns an error if a backup model can't be
// found. Otherwise ignores missing backup models.
//
// Missing models or snapshots during the actual deletion do not cause errors.
//
// All backups are delete as an atomic unit so any failures will result in no
// deletions.
func (r repository) DeleteBackups(
	ctx context.Context,
	failOnMissing bool,
	ids ...string,
) error {
	return deleteBackups(ctx, store.NewWrapper(r.modelStore), failOnMissing, ids...)
}

// deleteBackup handles the processing for backup deletion.
func deleteBackups(
	ctx context.Context,
	sw store.BackupGetterModelDeleter,
	failOnMissing bool,
	ids ...string,
) error {
	// Although we haven't explicitly stated it, snapshots are technically
	// manifests in kopia. This means we can use the same delete API to remove
	// them and backup models. Deleting all of them together gives us both
	// atomicity guarantees (around when data will be flushed) and helps reduce
	// the number of manifest blobs that kopia will create.
	var toDelete []manifest.ID

	for _, id := range ids {
		b, err := sw.GetBackup(ctx, model.StableID(id))
		if err != nil {
			if !failOnMissing && errors.Is(err, data.ErrNotFound) {
				continue
			}

			return clues.Stack(errWrapper(err)).
				WithClues(ctx).
				With("delete_backup_id", id)
		}

		toDelete = append(toDelete, b.ModelStoreID)

		if len(b.SnapshotID) > 0 {
			toDelete = append(toDelete, manifest.ID(b.SnapshotID))
		}

		ssid := b.StreamStoreID
		if len(ssid) == 0 {
			ssid = b.DetailsID
		}

		if len(ssid) > 0 {
			toDelete = append(toDelete, manifest.ID(ssid))
		}
	}

	return sw.DeleteWithModelStoreIDs(ctx, toDelete...)
}

func (r repository) ConnectToM365(
	ctx context.Context,
	pst path.ServiceType,
) (*m365.Controller, error) {
	ctrl, err := connectToM365(ctx, pst, r.Account, r.Opts)
	if err != nil {
		return nil, clues.Wrap(err, "connecting to m365")
	}

	return ctrl, nil
}

// ---------------------------------------------------------------------------
// Repository ID Model
// ---------------------------------------------------------------------------

// repositoryModel identifies the current repository
type repositoryModel struct {
	model.BaseModel
}

// should only be called on init.
func newRepoModel(ctx context.Context, ms *kopia.ModelStore, repoID string) error {
	rm := repositoryModel{
		BaseModel: model.BaseModel{
			ID: model.StableID(repoID),
		},
	}

	return ms.Put(ctx, model.RepositorySchema, &rm)
}

// retrieves the repository info
func getRepoModel(ctx context.Context, ms *kopia.ModelStore) (*repositoryModel, error) {
	bms, err := ms.GetIDsForType(ctx, model.RepositorySchema, nil)
	if err != nil {
		return nil, err
	}

	rm := &repositoryModel{}
	if len(bms) == 0 {
		return rm, nil
	}

	rm.BaseModel = *bms[0]

	return rm, nil
}

// newRepoID generates a new unique repository id hash.
// Repo IDs should only be generated once per repository,
// and must be stored after that.
func newRepoID(s storage.Storage) string {
	return uuid.NewString()
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

var m365nonce bool

func connectToM365(
	ctx context.Context,
	pst path.ServiceType,
	acct account.Account,
	co control.Options,
) (*m365.Controller, error) {
	if !m365nonce {
		m365nonce = true

		progressBar := observe.MessageWithCompletion(ctx, "Connecting to M365")
		defer close(progressBar)
	}

	ctrl, err := m365.NewController(ctx, acct, pst, co)
	if err != nil {
		return nil, err
	}

	return ctrl, nil
}

func errWrapper(err error) error {
	if errors.Is(err, data.ErrNotFound) {
		return clues.Stack(ErrorBackupNotFound, err)
	}

	return err
}
