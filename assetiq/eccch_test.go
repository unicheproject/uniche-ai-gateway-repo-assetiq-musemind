package assetiq

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"uniche/api/repositoryapi"
	"uniche/connectors/eccch"
)

// TestEccchHandlesTurnIntoHDTURIs checks that a curator's ECCCH handle names
// the right digital twin, and that anything malformed is refused before it
// can reach a SPARQL query.
func TestEccchHandlesTurnIntoHDTURIs(t *testing.T) {
	//GIVEN: a valid handle for the Galassi monument.

	//WHEN: it is parsed.
	uri, err := parseEccchURI("assetiq://eccch/hdt/Fkp1JTtfVek")

	//THEN: it names the HDT in the ECHOES namespace.
	require.NoError(t, err)
	assert.Equal(t, "http://echoes-eccch.eu/HDT/Fkp1JTtfVek", uri)

	for name, bad := range map[string]string{
		"wrong prefix":       "assetiq://europeana/hdt/Fkp1JTtfVek",
		"empty id":           "assetiq://eccch/hdt/",
		"nested path":        "assetiq://eccch/hdt/a/b",
		"query injection":    "assetiq://eccch/hdt/x> } ; DROP",
		"angle bracket only": "assetiq://eccch/hdt/x>",
	} {
		//GIVEN: a malformed handle.

		//WHEN: it is parsed.
		_, err := parseEccchURI(bad)

		//THEN: it is rejected as an invalid handle.
		assert.ErrorIs(t, err, ErrInvalidURI, name)
	}
}

// TestAnHDTMapsToARepositoryAsset checks that the Galassi monument becomes a
// read-only 3D asset with its label, description, rights and model file, and
// that a vague "octet-stream" format is left for the file server to decide.
func TestAnHDTMapsToARepositoryAsset(t *testing.T) {
	//GIVEN: the Galassi HDT with an Italian ownership statement as rights.
	hdt := galassiHDT()

	//WHEN: it is mapped.
	src, err := mapEccch(hdt)

	//THEN: every field from the mapping table is set.
	require.NoError(t, err)
	in := src.ingest
	assert.Equal(t, repositoryapi.IngestRequestSourceEccch, in.Source)
	assert.Equal(t, repositoryapi.IngestRequestExternalId{Provider: "eccch", Identifier: hdt.URI}, in.ExternalId)
	assert.Equal(t, "Allegoria della disperazione - Elio Galassi", in.Metadata.Title)
	assert.Equal(t, "Monumento ai caduti", in.Metadata.Description.Value)
	assert.Equal(t, repositoryapi.AssetType3d, in.Metadata.Type)
	assert.Equal(t, "proprietà Ente pubblico territoriale", in.Metadata.Rights.Value)
	assert.False(t, in.Metadata.Creator.Set)
	assert.False(t, in.Metadata.Date.Set)
	assert.False(t, in.RawSource.Set)
	assert.True(t, in.ReadOnly.Value, "an ownership statement is not an open licence")
	assert.Equal(t, hdt.Models[0].URL, src.fileURL)
	assert.Empty(t, src.fileMediaType)
}

// TestAnHDTKeepsItsDatePlaceAndRawRecord checks that the monument's date and
// place reach the asset, and that the triples ECHOES sent are kept as the
// asset's raw source, unchanged.
func TestAnHDTKeepsItsDatePlaceAndRawRecord(t *testing.T) {
	//GIVEN: the Galassi HDT dated 1924, in Pisa, with its raw triples.
	hdt := galassiHDT()
	hdt.Date = "1924-1924"
	hdt.Coverage = "Pisa (PI)"
	hdt.Raw = []byte(`{"succeed":true,"results":{}}`)

	//WHEN: it is mapped.
	src, err := mapEccch(hdt)

	//THEN: date, location and raw source are set from it.
	require.NoError(t, err)
	assert.Equal(t, "1924-1924", src.ingest.Metadata.Date.Value)
	assert.Equal(t, "Pisa (PI)", src.ingest.Metadata.Location.Value)
	assert.Equal(t, `{"succeed":true,"results":{}}`, src.ingest.RawSource.Value)
}

// TestAnOpenlyLicensedHDTIsEditable checks that an HDT under CC BY imports
// editable, and that a stated model format is kept.
func TestAnOpenlyLicensedHDTIsEditable(t *testing.T) {
	//GIVEN: the Galassi HDT under CC BY 4.0 with a glTF model format.
	hdt := galassiHDT()
	hdt.Rights = "https://creativecommons.org/licenses/by/4.0/"
	hdt.Models[0].Format = "model/gltf-binary"

	//WHEN: it is mapped.
	src, err := mapEccch(hdt)

	//THEN: it is editable and keeps the stated format.
	require.NoError(t, err)
	assert.False(t, src.ingest.ReadOnly.Value)
	assert.Equal(t, "model/gltf-binary", src.fileMediaType)
}

// TestAnHDTWithoutAModelIsRejected checks that a digital twin with no 3D
// model file is not imported as an empty asset.
func TestAnHDTWithoutAModelIsRejected(t *testing.T) {
	//GIVEN: an HDT with no models.
	hdt := galassiHDT()
	hdt.Models = nil

	//WHEN: it is mapped.
	_, err := mapEccch(hdt)

	//THEN: mapping fails, naming the missing model.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no 3D model")
}

// TestEccchImportFailsClearlyWhenNotConfigured checks that without an EGI
// refresh token a valid ECCCH handle is refused as a server setting, not as
// the caller's mistake, with a message saying how to enable it.
func TestEccchImportFailsClearlyWhenNotConfigured(t *testing.T) {
	//GIVEN: an importer with no ECCCH client.
	imp := NewImporter(nil, nil, nil)

	//WHEN: an ECCCH handle is imported.
	_, err := imp.Import(context.Background(), "assetiq://eccch/hdt/Fkp1JTtfVek")

	//THEN: it is refused as not configured, naming the missing setting.
	require.ErrorIs(t, err, ErrSourceNotConfigured)
	assert.NotErrorIs(t, err, ErrInvalidURI)
	assert.Contains(t, err.Error(), "EGI_REFRESH_TOKEN")
}

func galassiHDT() *eccch.HDT {
	return &eccch.HDT{
		URI:         "http://echoes-eccch.eu/HDT/Fkp1JTtfVek",
		Label:       "Allegoria della disperazione - Elio Galassi",
		Entity:      "https://w3id.org/arco/resource/HistoricOrArtisticProperty/0901078520",
		Description: "Monumento ai caduti",
		Rights:      "proprietà Ente pubblico territoriale",
		Models: []eccch.Model{{
			URL:    "https://vicserver.crs4.it/ocra-assets/Galassi-Allegoria_della_disperazione-Scan2014.glb",
			Format: "application/octet-stream",
		}},
	}
}
