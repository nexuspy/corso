package drive

import (
	"github.com/alcionai/clues"

	odConsts "github.com/alcionai/corso/src/internal/m365/service/onedrive/consts"
	"github.com/alcionai/corso/src/pkg/path"
	"github.com/alcionai/corso/src/pkg/selectors"
	"github.com/alcionai/corso/src/pkg/services/m365/api"
)

var _ BackupHandler = &groupBackupHandler{}

type groupBackupHandler struct {
	libraryBackupHandler
	groupID string
	scope   selectors.GroupsScope
}

func NewGroupBackupHandler(
	groupID, siteID string,
	ac api.Drives,
	scope selectors.GroupsScope,
) groupBackupHandler {
	return groupBackupHandler{
		libraryBackupHandler{
			ac:     ac,
			siteID: siteID,
			// Not adding scope here. Anything that needs scope has to
			// be from group handler
			service: path.GroupsService,
		},
		groupID,
		scope,
	}
}

func (h groupBackupHandler) PathPrefix(
	tenantID, driveID string,
) (path.Path, error) {
	// TODO: move tenantID to struct
	return path.Build(
		tenantID,
		h.groupID,
		h.service,
		path.LibrariesCategory,
		false,
		odConsts.SitesPathDir,
		h.siteID,
		odConsts.DrivesPathDir,
		driveID,
		odConsts.RootPathDir)
}

func (h groupBackupHandler) MetadataPathPrefix(tenantID string) (path.Path, error) {
	p, err := path.BuildMetadata(
		tenantID,
		h.groupID,
		h.service,
		path.LibrariesCategory,
		false)
	if err != nil {
		return nil, clues.Wrap(err, "making metadata path")
	}

	p, err = p.Append(false, odConsts.SitesPathDir, h.siteID)
	if err != nil {
		return nil, clues.Wrap(err, "appending site id to metadata path")
	}

	return p, nil
}

func (h groupBackupHandler) CanonicalPath(
	folders *path.Builder,
	tenantID string,
) (path.Path, error) {
	return folders.ToDataLayerPath(
		tenantID,
		h.groupID,
		h.service,
		path.LibrariesCategory,
		false,
		odConsts.SitesPathDir,
		h.siteID)
}

func (h groupBackupHandler) SitePathPrefix(tenantID string) (path.Path, error) {
	return path.Build(
		tenantID,
		h.groupID,
		h.service,
		path.LibrariesCategory,
		false,
		odConsts.SitesPathDir,
		h.siteID)
}

func (h groupBackupHandler) IsAllPass() bool {
	return h.scope.IsAny(selectors.GroupsLibraryFolder)
}

func (h groupBackupHandler) IncludesDir(dir string) bool {
	return h.scope.Matches(selectors.GroupsLibraryFolder, dir)
}
