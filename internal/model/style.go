package model

// Style is a fully resolved character style. Tri-state deltas exist only
// during frontend resolution; by the time content reaches the model every
// toggle has a definite value.
//
// The zero value is the plain style: no toggle set (upstream Style::PLAIN).
type Style struct {
	// Bold weight.
	Bold bool
	// Italic or oblique.
	Italic bool
	// Strike: struck through.
	Strike bool
	// Code: monospace, from a code or teletype character style.
	Code bool
}
