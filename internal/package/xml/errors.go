// Error values and renderings for the tokenizer and namespace resolver,
// matching quick-xml 0.41's Error Display strings byte for byte (anydoc
// surfaces them as "unparseable xml: {e}").
package xml

import (
	"errors"
	"fmt"
	"strings"
)

var (
	errInvalidBang         = errors.New("syntax error: unknown or missed symbol in markup")
	errUnclosedPI          = errors.New("syntax error: processing instruction not closed: `?>` not found before end of input")
	errUnclosedXmlDecl     = errors.New("syntax error: XML declaration not closed: `?>` not found before end of input")
	errUnclosedComment     = errors.New("syntax error: comment not closed: `-->` not found before end of input")
	errUnclosedDoctype     = errors.New("syntax error: DOCTYPE not closed: `>` not found before end of input")
	errUnclosedCData       = errors.New("syntax error: CDATA not closed: `]]>` not found before end of input")
	errUnclosedTag         = errors.New("syntax error: tag not closed: `>` not found before end of input")
	errUnclosedSingleQuote = errors.New("syntax error: attribute value not closed: `'` not found before end of input")
	errUnclosedDoubleQuote = errors.New("syntax error: attribute value not closed: `\"` not found before end of input")
	errMissingDoctypeName  = errors.New("ill-formed document: `<!DOCTYPE>` declaration does not contain a name of a document type")
	errUnclosedReference   = errors.New("ill-formed document: entity or character reference not closed: `;` not found before end of input")
)

// errUnmatchedEndTag renders IllFormedError::UnmatchedEndTag. The name is
// the lossy-decoded end-tag content.
func errUnmatchedEndTag(name string) error {
	return fmt.Errorf("ill-formed document: close tag `</%s>` does not match any open tag", name)
}

// Namespace binding errors (NamespaceError Display; no Error wrapper prefix).
func errInvalidXmlPrefixBind(namespace []byte) error {
	return fmt.Errorf("the namespace prefix 'xml' cannot be bound to '%s'", quoteBytes(namespace))
}

func errInvalidXmlnsPrefixBind(namespace []byte) error {
	return fmt.Errorf("the namespace prefix 'xmlns' cannot be bound to '%s'", quoteBytes(namespace))
}

func errInvalidPrefixForXml(prefix []byte) error {
	return fmt.Errorf("the namespace prefix '%s' cannot be bound to 'http://www.w3.org/XML/1998/namespace'", quoteBytes(prefix))
}

func errInvalidPrefixForXmlns(prefix []byte) error {
	return fmt.Errorf("the namespace prefix '%s' cannot be bound to 'http://www.w3.org/2000/xmlns/'", quoteBytes(prefix))
}

func errTooManyDeclarations(limit int) error {
	return fmt.Errorf("start tag declares more than %d namespace bindings; raise the limit with NamespaceResolver::set_max_declarations_per_element", limit)
}

// quoteBytes renders bytes the way quick-xml's write_byte_string does:
// printable ASCII (except '"') as characters, '"' escaped, and everything
// else as 0xNN.
func quoteBytes(b []byte) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, c := range b {
		switch {
		case c >= 32 && c <= 33, c >= 35 && c <= 126:
			sb.WriteByte(c)
		case c == 34:
			sb.WriteString("\\\"")
		default:
			fmt.Fprintf(&sb, "0x%02X", c)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}
