// Hand-rolled streaming XML tokenizer reproducing quick-xml 0.41's slice
// reader event semantics (anydoc's configuration: check_end_names disabled,
// every other default kept). Event boundaries, recovery rules, and error
// strings match the upstream crate byte for byte; encoding/xml's divergent
// event semantics are why this exists.
package xml

import "github.com/m7medVision/anydoc-go/internal/text"

// tokenKind is one lexical XML event.
type tokenKind uint8

const (
	tokEOF tokenKind = iota
	tokStart
	tokEmpty
	tokEnd
	tokText
	tokRef
	tokCData
	// tokIgnored covers comments, processing instructions, XML declarations,
	// and DOCTYPE declarations: events the DOM builder drops.
	tokIgnored
)

// token is one tokenizer event. For Start and Empty, data holds the tag
// content between '<' and '>' without the closing bracket ("name attr=...");
// for End it holds the end-tag content with trailing whitespace trimmed. For
// Text, Ref, and CData it holds the raw undecoded bytes.
type token struct {
	kind tokenKind
	data []byte
}

// tokenizer walks a UTF-8 transcoded XML buffer, tracking open-tag depth so
// an end tag with no matching start is a hard error (quick-xml's
// allow_unmatched_ends=false).
type tokenizer struct {
	buf   []byte
	pos   int
	depth int
}

// next returns the next event. Every error's text is quick-xml 0.41's Error
// Display rendering.
func (t *tokenizer) next() (token, error) {
	for {
		if t.pos >= len(t.buf) {
			return token{kind: tokEOF}, nil
		}
		switch t.buf[t.pos] {
		case '<':
			return t.markup()
		case '&':
			return t.ref()
		default:
			// Text runs up to the next '<' or '&'; neither terminator is
			// part of the event. Empty text events never occur in the slice
			// reader, so the run is always non-empty here.
			start := t.pos
			for t.pos < len(t.buf) && t.buf[t.pos] != '<' && t.buf[t.pos] != '&' {
				t.pos++
			}
			return token{kind: tokText, data: t.buf[start:t.pos]}, nil
		}
	}
}

// ref reads a general entity or character reference at '&'. The reference
// must close with ';' before any markup, another '&', or end of input.
func (t *tokenizer) ref() (token, error) {
	// t.pos is at '&'; scan from the next byte.
	for i := t.pos + 1; i < len(t.buf); i++ {
		switch t.buf[i] {
		case ';':
			content := t.buf[t.pos+1 : i]
			t.pos = i + 1
			return token{kind: tokRef, data: content}, nil
		case '&', '<':
			return token{}, errUnclosedReference
		}
	}
	return token{}, errUnclosedReference
}

// markup reads one markup construct starting at '<'.
func (t *tokenizer) markup() (token, error) {
	lt := t.pos
	if lt+1 >= len(t.buf) {
		return token{}, errUnclosedTag
	}
	switch t.buf[lt+1] {
	case '!':
		return t.bang(lt)
	case '/':
		return t.endTag(lt)
	case '?':
		return t.pi(lt)
	default:
		return t.startTag(lt)
	}
}

// findGtOutsideQuotes locates the '>' ending a tag, honoring ElementParser's
// quoted regions: a quote opens a region outside values, and only a matching
// quote exits it. Input ending inside a region is an error naming the quote.
func (t *tokenizer) findGtOutsideQuotes(from int) (int, error) {
	const (
		outside = iota
		singleQ
		doubleQ
	)
	state := outside
	for i := from; i < len(t.buf); i++ {
		switch state {
		case outside:
			switch t.buf[i] {
			case '>':
				return i, nil
			case '\'':
				state = singleQ
			case '"':
				state = doubleQ
			}
		case singleQ:
			if t.buf[i] == '\'' {
				state = outside
			}
		case doubleQ:
			if t.buf[i] == '"' {
				state = outside
			}
		}
	}
	switch state {
	case singleQ:
		return 0, errUnclosedSingleQuote
	case doubleQ:
		return 0, errUnclosedDoubleQuote
	default:
		return 0, errUnclosedTag
	}
}

// startTag reads '<name ...>' or '<name .../>' at lt.
func (t *tokenizer) startTag(lt int) (token, error) {
	gt, err := t.findGtOutsideQuotes(lt)
	if err != nil {
		return token{}, err
	}
	raw := t.buf[lt : gt+1] // includes '<' and '>'
	t.pos = gt + 1
	content := raw[1:]
	if len(content) >= 2 && content[len(content)-2] == '/' && content[len(content)-1] == '>' {
		// Self-closed tag <something/>: an Empty event, no depth push.
		return token{kind: tokEmpty, data: content[:len(content)-2]}, nil
	}
	t.depth++
	return token{kind: tokStart, data: content[:len(content)-1]}, nil
}

// endTag reads '</name ...>' at lt. Trailing whitespace is trimmed from the
// content (trim_markup_names_in_closing_tags), except that content made of
// whitespace alone stays whole. A close with no open tag is an error even
// with end-name checking disabled (allow_unmatched_ends=false).
func (t *tokenizer) endTag(lt int) (token, error) {
	gt, err := t.findGtOutsideQuotes(lt)
	if err != nil {
		return token{}, err
	}
	raw := t.buf[lt : gt+1]
	t.pos = gt + 1
	content := raw[2 : len(raw)-1]
	for end := len(content); end > 0; end-- {
		if !isWhitespace(content[end-1]) {
			content = content[:end]
			break
		}
	}
	if t.depth == 0 {
		return token{}, errUnmatchedEndTag(text.DecodeUTF8(content))
	}
	t.depth--
	return token{kind: tokEnd, data: content}, nil
}

// pi reads a processing instruction or XML declaration at lt, ending at the
// first '?>'. Both event kinds are ignored by the DOM builder; only their
// errors surface.
func (t *tokenizer) pi(lt int) (token, error) {
	gt := -1
	for i := lt + 1; i < len(t.buf); i++ {
		if t.buf[i] == '>' && t.buf[i-1] == '?' {
			gt = i
			break
		}
	}
	if gt < 0 {
		return token{}, piEOFError(t.buf[lt:])
	}
	t.pos = gt + 1
	return token{kind: tokIgnored}, nil
}

// bang reads '<!' constructs: comments, CDATA sections, and DOCTYPE
// declarations.
func (t *tokenizer) bang(lt int) (token, error) {
	if lt+2 >= len(t.buf) {
		return token{}, errInvalidBang
	}
	switch t.buf[lt+2] {
	case '[':
		return t.cdata(lt)
	case '-':
		return t.comment(lt)
	case 'D', 'd':
		return t.docType(lt)
	default:
		return token{}, errInvalidBang
	}
}

// cdata reads a <![CDATA[...]]> section at lt. The section ends at the first
// '>' whose two preceding bytes are ']]'.
func (t *tokenizer) cdata(lt int) (token, error) {
	gt := -1
	for i := lt + 2; i < len(t.buf); i++ {
		if t.buf[i] == '>' && i-2 > lt && t.buf[i-2] == ']' && t.buf[i-1] == ']' {
			gt = i
			break
		}
	}
	if gt < 0 {
		return token{}, errUnclosedCData
	}
	raw := t.buf[lt : gt+1]
	t.pos = gt + 1
	if len(raw) < 9 || string(raw[:9]) != "<![CDATA[" {
		// A '[' bang that never spelled CDATA: quick-xml reports the
		// section as unterminated.
		return token{}, errUnclosedCData
	}
	return token{kind: tokCData, data: raw[9 : len(raw)-3]}, nil
}

// comment reads a <!--...--> comment at lt. The comment ends at the first
// '>' preceded by '--' with at least six bytes before it (quick-xml's
// "need to read at least 6 symbols" rule covering `<!---->`).
func (t *tokenizer) comment(lt int) (token, error) {
	gt := -1
	for i := lt + 1; i < len(t.buf); i++ {
		if t.buf[i] == '>' && i > lt+5 && t.buf[i-2] == '-' && t.buf[i-1] == '-' {
			gt = i
			break
		}
	}
	if gt < 0 {
		return token{}, errUnclosedComment
	}
	raw := t.buf[lt : gt+1]
	t.pos = gt + 1
	if len(raw) < 4 || string(raw[:4]) != "<!--" {
		return token{}, errUnclosedComment
	}
	return token{kind: tokIgnored}, nil
}

// Internal-subset states of the DOCTYPE scanner (quick-xml DtdParser).
const (
	dtBefore = iota // outside quotes and the internal subset
	dtQuoted
	dtInside     // inside [...] (intSubset)
	dtAfter      // after ']' waiting for '>'
	dtComment    // inside a comment in the subset
	dtPi         // inside a processing instruction in the subset
	dtElemDecl   // inside <!ELEMENT ...> (no quoting)
	dtQuotedDecl // inside <!ATTLIST/<!ENTITY/<!NOTATION ...> (quoted regions)
)

// docType reads a <!DOCTYPE...> declaration at lt, porting quick-xml's
// DtdParser: quoted strings, the internal subset [...], and the markup
// keywords recognized inside it are skipped so only the declaration's final
// '>' ends the scan.
func (t *tokenizer) docType(lt int) (token, error) {
	state := dtBefore
	var quote byte
	i := lt
	commentStart := 0
	for i < len(t.buf) {
		switch state {
		case dtBefore:
			switch t.buf[i] {
			case '\'', '"':
				quote = t.buf[i]
				state = dtQuoted
				i++
			case '[':
				state = dtInside
				i++
			case '>':
				return t.emitDocType(lt, i)
			default:
				i++
			}
		case dtQuoted:
			if t.buf[i] == quote {
				state = dtBefore
			}
			i++
		case dtInside:
			switch t.buf[i] {
			case ']':
				state = dtAfter
				i++
			case '<':
				rest := t.buf[i+1:]
				switch {
				case len(rest) >= 1 && rest[0] == '?':
					state = dtPi // scans from '<' like PiParser does
				case len(rest) >= 3 && rest[0] == '!' && rest[1] == '-' && rest[2] == '-':
					state = dtComment
					commentStart = i + 4 // first byte after "<!--"
					i += 4
				case len(rest) >= 8 && string(rest[:8]) == "!ELEMENT":
					state = dtElemDecl
					i += 8
				case len(rest) >= 7 && string(rest[:7]) == "!ENTITY":
					state = dtQuotedDecl
					quote = 0
					i += 7
				case len(rest) >= 8 && string(rest[:8]) == "!ATTLIST":
					state = dtQuotedDecl
					quote = 0
					i += 8
				case len(rest) >= 9 && string(rest[:9]) == "!NOTATION":
					state = dtQuotedDecl
					quote = 0
					i += 9
				case len(rest) >= 9:
					// Nine bytes after '<' without a known keyword: skip to
					// the next '>'.
					state = dtElemDecl
				default:
					// Fewer than nine bytes and no keyword prefix: the
					// markup cannot be decided before input ends.
					return token{}, errUnclosedDoctype
				}
			default:
				i++
			}
		case dtAfter:
			if t.buf[i] == '>' {
				return t.emitDocType(lt, i)
			}
			i++
		case dtComment:
			// Comments in the subset end at the first '>' preceded by '--'
			// where both dashes follow the comment's opening "<!--".
			if t.buf[i] == '>' && i-2 >= commentStart && t.buf[i-2] == '-' && t.buf[i-1] == '-' {
				state = dtInside
			}
			i++
		case dtPi:
			if t.buf[i] == '>' && t.buf[i-1] == '?' {
				state = dtInside
			}
			i++
		case dtElemDecl:
			if t.buf[i] == '>' {
				state = dtInside
			}
			i++
		case dtQuotedDecl:
			// ATTLIST / ENTITY / NOTATION: '>' ends the declaration unless
			// it falls inside a quoted value.
			switch {
			case quote == 0:
				switch t.buf[i] {
				case '>':
					state = dtInside
				case '\'', '"':
					quote = t.buf[i]
				}
			default:
				if t.buf[i] == quote {
					quote = 0
				}
			}
			i++
		}
	}
	return token{}, errUnclosedDoctype
}

// emitDocType validates the finished <!DOCTYPE...> raw bytes and returns an
// ignored token; the declaration's name must be non-blank.
func (t *tokenizer) emitDocType(lt, gt int) (token, error) {
	raw := t.buf[lt : gt+1]
	t.pos = gt + 1
	if len(raw) < 9 || !asciiEqualFold(raw[:9], "<!DOCTYPE") {
		return token{}, errUnclosedDoctype
	}
	inner := raw[9 : len(raw)-1]
	allBlank := true
	for _, b := range inner {
		if !isWhitespace(b) {
			allBlank = false
			break
		}
	}
	if allBlank {
		return token{}, errMissingDoctypeName
	}
	return token{kind: tokIgnored}, nil
}

// piEOFError distinguishes an unterminated XML declaration from an
// unterminated processing instruction (PiParser::eof_error).
func piEOFError(content []byte) error {
	if len(content) >= 5 && string(content[:5]) == "<?xml" &&
		(len(content) == 5 || isWhitespace(content[5]) || content[5] == '?') {
		return errUnclosedXmlDecl
	}
	return errUnclosedPI
}

func isWhitespace(b byte) bool {
	return b == ' ' || b == '\r' || b == '\n' || b == '\t'
}

func asciiEqualFold(a []byte, s string) bool {
	if len(a) != len(s) {
		return false
	}
	for i := range a {
		if (a[i] | 0x20) != (s[i] | 0x20) {
			return false
		}
	}
	return true
}

// localName splits a qualified name at its first ':' and returns the local
// part (QName::local_name).
func localName(name []byte) []byte {
	for i, b := range name {
		if b == ':' {
			return name[i+1:]
		}
	}
	return name
}

// namePrefix splits a qualified name at its first ':' and returns the
// prefix, or nil when the name is unqualified (QName::decompose).
func namePrefix(name []byte) []byte {
	for i, b := range name {
		if b == ':' {
			return name[:i]
		}
	}
	return nil
}

// nameLen returns the element name's extent in tag content: the first word
// before XML whitespace (utils::name_len).
func nameLen(content []byte) int {
	for i, b := range content {
		if isWhitespace(b) {
			return i
		}
	}
	return len(content)
}
