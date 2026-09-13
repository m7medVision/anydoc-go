// Port of src/shared/mc.rs: OOXML markup-compatibility (mc:AlternateContent)
// branch selection.

package shared

import (
	"strings"

	"github.com/m7medVision/anydoc-go/internal/package/xml"
)

// AlternateBranch picks the branch of an mc:AlternateContent to process:
// the first mc:Choice whose Requires namespaces are all supported, else
// the mc:Fallback.
func AlternateBranch(alt *xml.Element, supported []string) *xml.Element {
	for c := range alt.FindAll(xml.NsMC, "Choice") {
		if choiceSupported(c, supported) {
			return c
		}
	}
	return alt.Find(xml.NsMC, "Fallback")
}

func choiceSupported(choice *xml.Element, supported []string) bool {
	requires, ok := choice.Attr(xml.NsMC, "Requires")
	if !ok {
		return true
	}
	// Requires prefixes were resolved to namespace URIs at parse time, in
	// the element's own lexical scope; an unresolved prefix stays literal
	// and matches nothing.
	for _, uri := range strings.Fields(requires) {
		if !containsStr(supported, uri) {
			return false
		}
	}
	return true
}

func containsStr(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
