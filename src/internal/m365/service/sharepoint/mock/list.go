package mock

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/alcionai/clues"
	kjson "github.com/microsoft/kiota-serialization-json-go"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/stretchr/testify/require"

	"github.com/alcionai/corso/src/internal/data"
	"github.com/alcionai/corso/src/pkg/fault"
	"github.com/alcionai/corso/src/pkg/path"
)

var (
	_ data.Item             = &ListData{}
	_ data.BackupCollection = &ListCollection{}
)

type ListCollection struct {
	fullPath path.Path
	Data     []*ListData
	Names    []string
}

func (mlc *ListCollection) SetPath(p path.Path) {
	mlc.fullPath = p
}

func (mlc *ListCollection) State() data.CollectionState {
	return data.NewState
}

func (mlc *ListCollection) FullPath() path.Path {
	return mlc.fullPath
}

func (mlc *ListCollection) DoNotMergeItems() bool {
	return false
}

func (mlc *ListCollection) PreviousPath() path.Path {
	return nil
}

func (mlc *ListCollection) Items(
	ctx context.Context,
	_ *fault.Bus, // unused
) <-chan data.Item {
	res := make(chan data.Item)

	go func() {
		defer close(res)

		for _, stream := range mlc.Data {
			res <- stream
		}
	}()

	return res
}

type ListData struct {
	ItemID  string
	Reader  io.ReadCloser
	ReadErr error
	size    int64
	deleted bool
}

func (mld *ListData) ID() string {
	return mld.ItemID
}

func (mld ListData) Deleted() bool {
	return mld.deleted
}

func (mld *ListData) ToReader() io.ReadCloser {
	return mld.Reader
}

// List returns a Listable object with two columns.
// @param: Name of the displayable list
// @param: Column Name: Defines the 2nd Column Name of the created list the values from the map.
// The key values of the input map are used for the `Title` column.
// The values of the map are placed within the 2nd column.
// Source: https://learn.microsoft.com/en-us/graph/api/list-create?view=graph-rest-1.0&tabs=go
func List(title, columnName string, items map[string]string) models.Listable {
	requestBody := models.NewList()
	requestBody.SetDisplayName(&title)
	requestBody.SetName(&title)

	columnDef := models.NewColumnDefinition()
	name := columnName
	text := models.NewTextColumn()

	columnDef.SetName(&name)
	columnDef.SetText(text)

	columns := []models.ColumnDefinitionable{
		columnDef,
	}
	requestBody.SetColumns(columns)

	aList := models.NewListInfo()
	template := "genericList"
	aList.SetTemplate(&template)
	requestBody.SetList(aList)

	// item Creation
	itms := make([]models.ListItemable, 0)

	for k, v := range items {
		entry := map[string]any{
			"Title":    k,
			columnName: v,
		}

		fields := models.NewFieldValueSet()
		fields.SetAdditionalData(entry)

		temp := models.NewListItem()
		temp.SetFields(fields)

		itms = append(itms, temp)
	}

	requestBody.SetItems(itms)

	return requestBody
}

// ListDefault returns a two-list column list of
// Music lbums and the associated artist.
func ListDefault(title string) models.Listable {
	return List(title, "Artist", getItems())
}

// ListBytes returns the byte representation of List
func ListBytes(title string) ([]byte, error) {
	list := ListDefault(title)

	objectWriter := kjson.NewJsonSerializationWriter()
	defer objectWriter.Close()

	err := objectWriter.WriteObjectValue("", list)
	if err != nil {
		return nil, err
	}

	return objectWriter.GetSerializedContent()
}

// ListStream returns the data.Item representation
// of the Mocked SharePoint List
func ListStream(t *testing.T, title string, numOfItems int) *ListData {
	byteArray, err := ListBytes(title)
	require.NoError(t, err, clues.ToCore(err))

	listData := &ListData{
		ItemID: title,
		Reader: io.NopCloser(bytes.NewReader(byteArray)),
		size:   int64(len(byteArray)),
	}

	return listData
}

// getItems returns a map where key values are albums
// and values are the artist.
// Source: https://github.com/Currie32/500-Greatest-Albums/blob/master/albumlist.csv
func getItems() map[string]string {
	items := map[string]string{
		"London Calling":                  "The Clash",
		"Blonde on Blonde":                "Bob Dylan",
		"The Beatles '(The White Album)'": "The Beatles",
		"The Sun Sessions":                "Elvis Presley",
		"Kind of Blue":                    "Miles Davis",
		"The Velvet Underground & Nico":   "The Velvet Underground",
		"Abbey Road":                      "The Beatles",
		"Are You Experienced":             "The Jimi Hendrix Experience",
		"Blood on the Tracks":             "Bob Dylan",
		"Nevermind":                       "Nirvana",
		"Born to Run":                     "Bruce Springsteen",
		"Astral Weeks":                    "Van Morrison",
		"Thriller":                        "Michael Jackson",
		"The Great Twenty_Eight":          "Chuck Berry",
		"The Complete Recordings":         "Robert Johnson",
		"John Lennon/Plastic Ono Band":    "John Lennon / Plastic Ono Band",
		"Innervisions":                    "Stevie Wonder",
		"Live at the Apollo, 1962":        "James Brown",
		"Rumours":                         "Fleetwood Mac",
		"The Joshua Tree":                 "U2",
	}

	return items
}
