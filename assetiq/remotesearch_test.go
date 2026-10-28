package assetiq

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"uniche/connectors/europeana"
)

// TestAnUnconfiguredSourceIsReportedAsSuch checks that searching ECCCH
// without a knowledge base client says "not configured", so the page can
// show that instead of a failure.
func TestAnUnconfiguredSourceIsReportedAsSuch(t *testing.T) {
	//GIVEN: a searcher with no ECCCH client.
	s := NewSearcher(europeana.NewClient("key"), nil)

	//WHEN: ECCCH is searched.
	_, err := s.Eccch(context.Background(), "galassi", 10)

	//THEN: the error is ErrSourceNotConfigured.
	assert.True(t, errors.Is(err, ErrSourceNotConfigured))
}

// TestEuropeanaHandleRoundTripsToTheRecordID checks that the import handle of
// a hit names the same record it was found as, so importing from search
// stores the identifier the Imported badge later looks up.
func TestEuropeanaHandleRoundTripsToTheRecordID(t *testing.T) {
	//GIVEN: the import handle built for a Europeana record.
	hit := europeanaHit("/90402/SK_A_2344", "The Milkmaid", "IMAGE", "")

	//WHEN: the handle is parsed the way the Importer does.
	id, err := parseEuropeanaURI(hit.ImportURI)

	//THEN: it gives back the record ID that is also the hit's identifier.
	require.NoError(t, err)
	assert.Equal(t, hit.Identifier, id)
}
