package runtime

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestOpenFGAModelSupportsFolderInheritance guards the authorization model JSON that the
// ACL-sync framework relies on (it can't be validated against a live OpenFGA in unit
// tests, so we at least assert it marshals and declares folder + document inheritance +
// nested group membership).
func TestOpenFGAModelSupportsFolderInheritance(t *testing.T) {
	b, err := json.Marshal(openFGAModel())
	if err != nil {
		t.Fatalf("authorization model must marshal to JSON: %v", err)
	}
	s := string(b)
	for _, want := range []string{
		`"folder"`,            // folder type exists
		`"parent"`,            // document -> folder parent relation
		"tupleToUserset",      // viewer-from-parent inheritance
		"computedUserset",     // ... resolves the folder's viewer
		`"relation":"member"`, // nested group#member references
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("authorization model missing %q in: %s", want, s)
		}
	}
}
