package path

import (
	"fmt"

	"github.com/alcionai/clues"

	"github.com/alcionai/corso/src/internal/common/pii"
)

var piiSafePathElems = pii.MapWithPlurals(
	// services
	UnknownService.String(),
	ExchangeService.String(),
	OneDriveService.String(),
	GroupsService.String(),
	SharePointService.String(),
	ExchangeMetadataService.String(),
	OneDriveMetadataService.String(),
	SharePointMetadataService.String(),
	GroupsMetadataService.String(),

	// categories
	UnknownCategory.String(),
	EmailCategory.String(),
	ContactsCategory.String(),
	EventsCategory.String(),
	FilesCategory.String(),
	ListsCategory.String(),
	LibrariesCategory.String(),
	PagesCategory.String(),
	DetailsCategory.String(),

	// well known folders
	// https://learn.microsoft.com/en-us/graph/api/resources/mailfolder?view=graph-rest-1.0
	"archive",
	"clutter",
	"conflict",
	"conversationhistory",
	"deleteditem",
	"draft",
	"inbox",
	"junkemail",
	"localfailure",
	"msgfolderroot",
	"outbox",
	"recoverableitemsdeletion",
	"scheduled",
	"searchfolder",
	"sentitem",
	"serverfailure",
	"syncissue")

var (
	// interface compliance required for handling PII
	_ clues.Concealer = &Elements{}
	_ fmt.Stringer    = &Elements{}
)

// Elements are a PII Concealer-compliant slice of elements within a path.
type Elements []string

// NewElements creates a new Elements slice by splitting the provided string.
func NewElements(p string) Elements {
	return Split(p)
}

// Conceal produces a concealed representation of the elements, suitable for
// logging, storing in errors, and other output.
func (el Elements) Conceal() string {
	escaped := make([]string, 0, len(el))

	for _, e := range el {
		escaped = append(escaped, escapeElement(e))
	}

	return join(pii.ConcealElements(escaped, piiSafePathElems))
}

// Format produces a concealed representation of the elements, even when
// used within a PrintF, suitable for logging, storing in errors,
// and other output.
func (el Elements) Format(fs fmt.State, _ rune) {
	fmt.Fprint(fs, el.Conceal())
}

// String returns a string that contains all path elements joined together.
// Elements that need escaping are escaped.  The result is not concealed, and
// is not suitable for logging or structured errors.
func (el Elements) String() string {
	escaped := make([]string, 0, len(el))

	for _, e := range el {
		escaped = append(escaped, escapeElement(e))
	}

	return join(escaped)
}

// PlainString returns an unescaped, unmodified string of the joined elements.
// The result is not concealed, and is not suitable for logging or structured
// errors.
func (el Elements) PlainString() string {
	return join(el)
}

// Last returns the last element.  Returns "" if empty.
func (el Elements) Last() string {
	if len(el) == 0 {
		return ""
	}

	return el[len(el)-1]
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// LoggableDir takes in a path reference (of any structure) and conceals any
// non-standard elements (ids, filenames, foldernames, etc).
func LoggableDir(ref string) string {
	// Can't directly use Builder since that could return an error. Instead split
	// into elements and use that.
	split := Split(TrimTrailingSlash(ref))
	return Elements(split).Conceal()
}
