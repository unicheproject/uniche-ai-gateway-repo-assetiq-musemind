package europeana

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSearchListsRecordsWithTheirThumbnails checks that a free-text search
// asks the Search API for the right number of rows and returns each record's
// ID, first title, type and preview image.
func TestSearchListsRecordsWithTheirThumbnails(t *testing.T) {
	//GIVEN: a Search API that answers with one record.
	var gotQuery, gotRows, gotKey string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotRows, gotKey = r.URL.Query().Get("query"), r.URL.Query().Get("rows"), r.Header.Get("X-Api-Key")
		io.WriteString(w, `{"success":true,"items":[{"id":"/90402/SK_A_2344","title":["The Milkmaid","De Melkmeid"],"type":"IMAGE","edmPreview":["https://thumb.example/1"]}]}`)
	}))
	defer api.Close()
	c := NewClient("key", WithBaseURL(api.URL))

	//WHEN: it is searched.
	hits, err := c.Search(context.Background(), "milk maid", 7)

	//THEN: the query is passed through and the record is mapped.
	require.NoError(t, err)
	assert.Equal(t, "milk maid", gotQuery)
	assert.Equal(t, "7", gotRows)
	assert.Equal(t, "key", gotKey)
	assert.Equal(t, []Hit{{ID: "/90402/SK_A_2344", Title: "The Milkmaid", Type: "IMAGE", Thumbnail: "https://thumb.example/1"}}, hits)
}
