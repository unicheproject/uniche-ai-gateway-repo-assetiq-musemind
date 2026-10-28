package assetiq

import (
	"errors"
	"fmt"
	"strings"

	"uniche/api/repositoryapi"
	"uniche/connectors/eccch"
)

const (
	eccchHDTPrefix    = "assetiq://eccch/hdt/"
	eccchHDTNamespace = "http://echoes-eccch.eu/HDT/"
)

// parseEccchURI turns assetiq://eccch/hdt/{local} into the HDT URI
// "http://echoes-eccch.eu/HDT/{local}".
func parseEccchURI(uri string) (string, error) {
	local, ok := strings.CutPrefix(uri, eccchHDTPrefix)
	if !ok || !handlePart.MatchString(local) {
		return "", fmt.Errorf("%w: want %s{local}, got %q", ErrInvalidURI, eccchHDTPrefix, uri)
	}
	return eccchHDTNamespace + local, nil
}

func mapEccch(hdt *eccch.HDT) (*source, error) {
	if hdt.Label == "" {
		return nil, errors.New("hdt has no label")
	}
	if len(hdt.Models) == 0 {
		return nil, errors.New("hdt has no 3D model")
	}
	model := hdt.Models[0]
	mediaType := model.Format
	if mediaType == "application/octet-stream" {
		// Too vague to be useful; let the file server's Content-Type win.
		mediaType = ""
	}

	return &source{
		ingest: &repositoryapi.IngestRequest{
			Source:     repositoryapi.IngestRequestSourceEccch,
			ExternalId: repositoryapi.IngestRequestExternalId{Provider: "eccch", Identifier: hdt.URI},
			ReadOnly:   repositoryapi.NewOptBool(!openRights(hdt.Rights)),
			Metadata: repositoryapi.AssetMetadata{
				Title:       hdt.Label,
				Description: optString(hdt.Description),
				Type:        repositoryapi.AssetType3d,
				Date:        optString(hdt.Date),
				Location:    optString(hdt.Coverage),
				Rights:      optString(hdt.Rights),
			},
			RawSource: optString(string(hdt.Raw)),
		},
		fileURL:       model.URL,
		fileMediaType: mediaType,
	}, nil
}
