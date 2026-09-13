// Port of src/shared/fields.rs: Word field-instruction parsing (DOC, DOCX,
// RTF), via a small case-insensitive lexer with known switch arities.

package shared

import (
	"strings"
	"unicode"

	"github.com/m7medVision/anydoc-go/internal/model"
)

// FieldFrame is a field accumulator: instruction text before the
// separator, result after.
type FieldFrame struct {
	Instr    string
	InResult bool
	Inlines  []model.Inline
}

// FieldResult finishes a field: wrap the result in a link when the
// instruction is a hyperlink, otherwise pass the result content through.
func FieldResult(instr string, content []model.Inline) []model.Inline {
	target, ok := HyperlinkTarget(instr)
	if ok && !model.InlinesAreEmpty(content) {
		return []model.Inline{model.Link{Inlines: content, Target: target}}
	}
	return content
}

type fieldTokenKind int

const (
	tokenWord fieldTokenKind = iota
	tokenSwitch
)

type fieldToken struct {
	kind    fieldTokenKind
	word    string
	switch_ rune
}

// Tokenize a field instruction: quoted strings (with \" and \\ escapes),
// backslash switches, bare words.
func tokenize(instr string) []fieldToken {
	rs := []rune(instr)
	var out []fieldToken
	i := 0
	for i < len(rs) {
		c := rs[i]
		if unicode.IsSpace(c) {
			i++
			continue
		}
		if c == '"' {
			i++
			var word []rune
			for i < len(rs) {
				c = rs[i]
				i++
				switch c {
				case '"':
					goto quoted
				case '\\':
					if i >= len(rs) {
						goto quoted
					}
					esc := rs[i]
					i++
					if esc == '"' || esc == '\\' {
						word = append(word, esc)
					} else {
						word = append(word, '\\', esc)
					}
				default:
					word = append(word, c)
				}
			}
		quoted:
			out = append(out, fieldToken{kind: tokenWord, word: string(word)})
			continue
		}
		if c == '\\' {
			i++
			if i < len(rs) {
				sw := rs[i]
				i++
				out = append(out, fieldToken{kind: tokenSwitch, switch_: asciiLowerRune(sw)})
			}
			continue
		}
		start := i
		for i < len(rs) && !unicode.IsSpace(rs[i]) {
			i++
		}
		out = append(out, fieldToken{kind: tokenWord, word: string(rs[start:i])})
	}
	return out
}

func hyperlinkSwitchTakesArg(switch_ rune) bool {
	return switch_ == 'l' || switch_ == 'o' || switch_ == 't'
}

// HyperlinkTarget interprets a HYPERLINK field instruction as a link target.
func HyperlinkTarget(instr string) (model.LinkTarget, bool) {
	tokens := tokenize(instr)
	if len(tokens) == 0 || tokens[0].kind != tokenWord || !asciiEqualFold(tokens[0].word, "HYPERLINK") {
		return nil, false
	}
	i := 1
	var url, anchor string
	haveURL, haveAnchor := false, false
	for i < len(tokens) {
		tok := tokens[i]
		i++
		if tok.kind == tokenWord {
			if !haveURL && strings.TrimSpace(tok.word) != "" {
				url = strings.TrimSpace(tok.word)
				haveURL = true
			}
			continue
		}
		// A switch's argument is the next token only when it is a word; a
		// following switch means the argument was omitted.
		var arg string
		haveArg := false
		if hyperlinkSwitchTakesArg(tok.switch_) && i < len(tokens) && tokens[i].kind == tokenWord {
			arg = tokens[i].word
			haveArg = true
			i++
		}
		if tok.switch_ == 'l' && haveArg && strings.TrimSpace(arg) != "" {
			anchor = strings.TrimSpace(arg)
			haveAnchor = true
		}
	}
	switch {
	case haveURL && haveAnchor:
		return classifyLink(url + "#" + anchor), true
	case haveURL:
		return classifyLink(url), true
	case haveAnchor:
		return model.AnchorLink{ID: anchor}, true
	default:
		return nil, false
	}
}

// ClassifyRelTarget classifies an OPC relationship target as a link target.
// External-mode targets are parsed like hyperlink field targets (scheme
// detection); internal-mode targets stay package-relative.
func ClassifyRelTarget(external bool, target string) model.LinkTarget {
	if external {
		return classifyLink(target)
	}
	return model.RelativeLink{URL: target}
}

func classifyLink(url string) model.LinkTarget {
	if rest, ok := strings.CutPrefix(url, "#"); ok {
		return model.AnchorLink{ID: rest}
	}
	if IsAbsoluteURI(url) {
		return model.ExternalLink{URL: url}
	}
	return model.RelativeLink{URL: url}
}
