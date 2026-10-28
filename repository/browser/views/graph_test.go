package views

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPickLabelPrefersEnglish checks which label the subject index shows when
// a resource is named in several languages: English first, then an untagged
// name, then the first language alphabetically.
func TestPickLabelPrefersEnglish(t *testing.T) {
	tests := map[string]struct {
		byLang map[string]string
		want   string
	}{
		"english wins":           {map[string]string{"nl": "Het melkmeisje", "": "Melkmeisje", "en": "The Milkmaid"}, "The Milkmaid"},
		"untagged before others": {map[string]string{"nl": "Het melkmeisje", "": "Melkmeisje"}, "Melkmeisje"},
		"first language a-z":     {map[string]string{"nl": "Het melkmeisje", "de": "Die Milchmagd", "fr": "La Laitiere"}, "Die Milchmagd"},
		"single language":        {map[string]string{"it": "Pisa"}, "Pisa"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			//GIVEN: a resource named in the languages of the case.
			//WHEN: the subject index picks its label.
			got := pickLabel(tc.byLang)

			//THEN: the preferred language's name is shown.
			assert.Equal(t, tc.want, got)
		})
	}
}
