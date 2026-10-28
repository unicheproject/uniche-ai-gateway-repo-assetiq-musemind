package europeana

import (
	"encoding/json"
	"fmt"
)

// LangMap is Europeana's language-keyed value list, e.g. {"en": ["The Milkmaid"]}.
// The key "def" holds values with no language.
type LangMap map[string][]string

// Object is the part of a record's "object" that callers map from.
type Object struct {
	About        string        `json:"about"` // e.g. "/90402/SK_A_2344"
	Type         string        `json:"type"`  // IMAGE, 3D, VIDEO, SOUND, TEXT
	Proxies      []Proxy       `json:"proxies"`
	Aggregations []Aggregation `json:"aggregations"`
	Agents       []Agent       `json:"agents"`
}

// Proxy carries descriptive metadata. The provider's own proxy has
// EuropeanaProxy false; the one with true holds Europeana's enrichments.
type Proxy struct {
	EuropeanaProxy bool    `json:"europeanaProxy"`
	DcTitle        LangMap `json:"dcTitle"`
	DcCreator      LangMap `json:"dcCreator"`
	DcDate         LangMap `json:"dcDate"`
	DctermsCreated LangMap `json:"dctermsCreated"`
}

type Aggregation struct {
	EdmRights    LangMap       `json:"edmRights"`
	EdmIsShownBy string        `json:"edmIsShownBy"`
	WebResources []WebResource `json:"webResources"`
}

type WebResource struct {
	About    string `json:"about"`
	MimeType string `json:"ebucoreHasMimeType"`
}

type Agent struct {
	PrefLabel LangMap `json:"prefLabel"`
}

func parseObject(body []byte) (*Object, error) {
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
		Object  Object `json:"object"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("europeana reported failure: %s", resp.Error)
	}
	return &resp.Object, nil
}
