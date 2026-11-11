// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package jsasset locates named constructs in the embedded frontend JavaScript.
//
// The card-render assertions need to read structure out of app.js — the keys of
// CARD_CONFIG, a mapper's returned object literal, a function body, or one
// branch inside a function — rather than grep for substrings. A substring match
// would be satisfied by a name mentioned in a comment or a string literal, so
// this package scans the source and skips comments, quoted strings and template
// literals before it matches anything. It is shared by the prompts that capture
// the pre-change card vectors and later prove the card did not regress.
package jsasset

import (
	stderrors "errors"
	"io/fs"
	"strings"

	staticui "github.com/bborbe/vault-ui/src/vault_ui"
)

var (
	// ErrAsset reports that the embedded frontend asset could not be read.
	ErrAsset = stderrors.New("read embedded frontend asset")
	// ErrNotFound reports that the named construct is absent from the source.
	ErrNotFound = stderrors.New("javascript construct not found")
	// ErrMalformed reports an unbalanced or otherwise unparseable construct.
	ErrMalformed = stderrors.New("malformed javascript construct")
)

// LoadAsset returns the embedded app.js source.
func LoadAsset() (string, error) {
	sub, err := fs.Sub(staticui.FS, "static")
	if err != nil {
		return "", ErrAsset
	}
	data, err := fs.ReadFile(sub, "app.js")
	if err != nil {
		return "", ErrAsset
	}
	return string(data), nil
}

// ObjectLiteral returns the key-to-source-expression map of the object literal
// assigned to the named `const`, `let` or `var`. Each value is the raw source
// text between the key's colon and the next top-level comma or the closing
// brace, trimmed but otherwise unmodified.
func ObjectLiteral(source, name string) (map[string]string, error) {
	open, close, err := findConstObject(source, name)
	if err != nil {
		return nil, err
	}
	return ParseObjectLiteral(source[open : close+1])
}

// ParseObjectLiteral parses an object-literal source text and returns its
// key-to-source-expression map. Nested objects, arrays, calls and template
// literals are kept whole in the value text.
func ParseObjectLiteral(expr string) (map[string]string, error) {
	start := skipWhitespaceAndComments(expr, 0)
	if start >= len(expr) || expr[start] != '{' {
		return nil, ErrMalformed
	}
	close := matchBrace(expr, start)
	if close < 0 {
		return nil, ErrMalformed
	}
	result := map[string]string{}
	i := start + 1
	for {
		i = skipWhitespaceAndComments(expr, i)
		if i >= close {
			break
		}
		if expr[i] == ',' {
			i++
			continue
		}
		key, after, keyErr := readObjectKey(expr, i)
		if keyErr != nil {
			return nil, keyErr
		}
		i = skipWhitespaceAndComments(expr, after)
		if i >= close || expr[i] != ':' {
			return nil, ErrMalformed
		}
		i = skipWhitespaceAndComments(expr, i+1)
		valueStart := i
		i = scanObjectValue(expr, i, close)
		result[key] = strings.TrimSpace(expr[valueStart:i])
	}
	return result, nil
}

// ReturnedObject returns the key-to-source-expression map of the object literal
// a named function returns at its top level.
func ReturnedObject(source, funcName string) (map[string]string, error) {
	bodyStart, bodyEnd, err := findFunctionBody(source, funcName)
	if err != nil {
		return nil, err
	}
	depth := 0
	i := bodyStart
	for i < bodyEnd {
		if next, ok := skipNonCode(source, i); ok {
			i = next
			continue
		}
		switch source[i] {
		case '{':
			depth++
		case '}':
			depth--
		}
		if depth == 0 && isIdentStart(source[i]) {
			word, end := readIdent(source, i)
			if word == "return" {
				value := skipWhitespaceAndComments(source, end)
				if value < bodyEnd && source[value] == '{' {
					close := matchBrace(source, value)
					if close < 0 {
						return nil, ErrMalformed
					}
					return ParseObjectLiteral(source[value : close+1])
				}
			}
			i = end
			continue
		}
		i++
	}
	return nil, ErrNotFound
}

// FunctionBody returns the text between the braces of the named function.
func FunctionBody(source, funcName string) (string, error) {
	bodyStart, bodyEnd, err := findFunctionBody(source, funcName)
	if err != nil {
		return "", err
	}
	return source[bodyStart:bodyEnd], nil
}

// Branch returns the text between the braces of the innermost block that holds
// marker inside the named function. A marker in an `if` condition (at the
// function-body level, before the block opens) selects the block that opens
// next, so the condition and its body together identify one branch. The marker
// is matched only in code: a copy in a comment or string literal does not count.
func Branch(source, funcName, marker string) (string, error) {
	bodyStart, bodyEnd, err := findFunctionBody(source, funcName)
	if err != nil {
		return "", err
	}
	markerAt := findCodeSubstring(source, marker, bodyStart, bodyEnd)
	if markerAt < 0 {
		return "", ErrNotFound
	}
	stack := make([]int, 0, 4)
	for i := bodyStart; i < markerAt; {
		if next, ok := skipNonCode(source, i); ok {
			i = next
			continue
		}
		switch source[i] {
		case '{':
			stack = append(stack, i)
		case '}':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
		i++
	}
	if len(stack) > 0 {
		open := stack[len(stack)-1]
		close := matchBrace(source, open)
		if close < 0 {
			return "", ErrMalformed
		}
		return source[open+1 : close], nil
	}
	for i := markerAt; i < bodyEnd; {
		if next, ok := skipNonCode(source, i); ok {
			i = next
			continue
		}
		if source[i] == '{' {
			close := matchBrace(source, i)
			if close < 0 {
				return "", ErrMalformed
			}
			return source[i+1 : close], nil
		}
		i++
	}
	return "", ErrNotFound
}

// CollapseWhitespace replaces every run of whitespace with a single space and
// trims the ends, so a fixture value stays stable under reformatting.
func CollapseWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// findConstObject returns the open and close brace positions of the object
// literal assigned to the named `const`, `let` or `var`.
func findConstObject(source, name string) (int, int, error) {
	for i := 0; i < len(source); {
		if next, ok := skipNonCode(source, i); ok {
			i = next
			continue
		}
		if !isIdentStart(source[i]) {
			i++
			continue
		}
		word, end := readIdent(source, i)
		if word != "const" && word != "let" && word != "var" {
			i = end
			continue
		}
		identifier := skipWhitespaceAndComments(source, end)
		if identifier >= len(source) || !isIdentStart(source[identifier]) {
			i = end
			continue
		}
		varName, after := readIdent(source, identifier)
		if varName != name {
			i = end
			continue
		}
		equals := skipWhitespaceAndComments(source, after)
		if equals >= len(source) || source[equals] != '=' {
			i = end
			continue
		}
		open := skipWhitespaceAndComments(source, equals+1)
		if open >= len(source) || source[open] != '{' {
			i = end
			continue
		}
		close := matchBrace(source, open)
		if close < 0 {
			return 0, 0, ErrMalformed
		}
		return open, close, nil
	}
	return 0, 0, ErrNotFound
}

// findFunctionBody returns the positions just inside the braces of the named
// function declaration.
func findFunctionBody(source, name string) (int, int, error) {
	for i := 0; i < len(source); {
		if next, ok := skipNonCode(source, i); ok {
			i = next
			continue
		}
		if !isIdentStart(source[i]) {
			i++
			continue
		}
		word, end := readIdent(source, i)
		if word != "function" {
			i = end
			continue
		}
		nameAt := skipWhitespaceAndComments(source, end)
		if nameAt < len(source) && source[nameAt] == '*' {
			nameAt = skipWhitespaceAndComments(source, nameAt+1)
		}
		if nameAt >= len(source) || !isIdentStart(source[nameAt]) {
			i = end
			continue
		}
		funcName, afterName := readIdent(source, nameAt)
		if funcName != name {
			i = end
			continue
		}
		paren := skipWhitespaceAndComments(source, afterName)
		if paren >= len(source) || source[paren] != '(' {
			i = end
			continue
		}
		closeParen := matchParen(source, paren)
		if closeParen < 0 {
			return 0, 0, ErrMalformed
		}
		open := skipWhitespaceAndComments(source, closeParen+1)
		if open >= len(source) || source[open] != '{' {
			i = end
			continue
		}
		close := matchBrace(source, open)
		if close < 0 {
			return 0, 0, ErrMalformed
		}
		return open + 1, close, nil
	}
	return 0, 0, ErrNotFound
}

// readObjectKey reads one object-literal key: a quoted string or a bare
// identifier. It returns the key text and the position just past it.
func readObjectKey(source string, i int) (string, int, error) {
	if source[i] == '\'' || source[i] == '"' {
		end := skipQuoted(source, i)
		if end > len(source) || end <= i+1 || source[end-1] != source[i] {
			return "", 0, ErrMalformed
		}
		return source[i+1 : end-1], end, nil
	}
	if isIdentStart(source[i]) {
		key, end := readIdent(source, i)
		return key, end, nil
	}
	end := i
	for end < len(source) && source[end] != ':' && !isSpace(source[end]) {
		end++
	}
	if end == i {
		return "", 0, ErrMalformed
	}
	return source[i:end], end, nil
}

// scanObjectValue advances past one object-literal value, stopping at the comma
// or closing brace that delimits it at the current nesting level.
func scanObjectValue(source string, i, limit int) int {
	depth := 0
	for i < limit {
		if next, ok := skipNonCode(source, i); ok {
			i = next
			continue
		}
		switch source[i] {
		case '{', '(', '[':
			depth++
		case '}', ')', ']':
			if depth == 0 {
				return i
			}
			depth--
		case ',':
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return i
}

// findCodeSubstring returns the first occurrence of sub in source between from
// and to that is not inside a comment, string or template literal.
func findCodeSubstring(source, sub string, from, to int) int {
	for i := from; i < to; {
		if next, ok := skipNonCode(source, i); ok {
			i = next
			continue
		}
		if strings.HasPrefix(source[i:], sub) {
			return i
		}
		i++
	}
	return -1
}

// skipNonCode advances past a comment, quoted string or template literal that
// starts at i. ok is false when no such construct starts there.
func skipNonCode(source string, i int) (int, bool) {
	if i+1 < len(source) && source[i] == '/' && source[i+1] == '/' {
		i += 2
		for i < len(source) && source[i] != '\n' {
			i++
		}
		return i, true
	}
	if i+1 < len(source) && source[i] == '/' && source[i+1] == '*' {
		i += 2
		for i+1 < len(source) && !(source[i] == '*' && source[i+1] == '/') {
			i++
		}
		if i+1 < len(source) {
			i += 2
		} else {
			i = len(source)
		}
		return i, true
	}
	switch source[i] {
	case '\'', '"':
		return skipQuoted(source, i), true
	case '`':
		return skipTemplate(source, i), true
	}
	return i, false
}

// skipQuoted advances past a single- or double-quoted string.
func skipQuoted(source string, i int) int {
	quote := source[i]
	i++
	for i < len(source) {
		switch source[i] {
		case '\\':
			i += 2
		case quote:
			return i + 1
		default:
			i++
		}
	}
	return len(source)
}

// skipTemplate advances past a template literal, including any nested
// `${...}` expressions.
func skipTemplate(source string, i int) int {
	i++
	for i < len(source) {
		switch {
		case source[i] == '\\':
			i += 2
		case source[i] == '`':
			return i + 1
		case source[i] == '$' && i+1 < len(source) && source[i+1] == '{':
			i = skipTemplateExpression(source, i+2)
		default:
			i++
		}
	}
	return len(source)
}

// skipTemplateExpression advances past the body of a `${...}` expression.
func skipTemplateExpression(source string, i int) int {
	depth := 1
	for i < len(source) {
		if next, ok := skipNonCode(source, i); ok {
			i = next
			continue
		}
		switch source[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
		i++
	}
	return len(source)
}

// matchBrace returns the position of the brace matching the one at open.
func matchBrace(source string, open int) int {
	depth := 0
	for i := open; i < len(source); {
		if next, ok := skipNonCode(source, i); ok {
			i = next
			continue
		}
		switch source[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return -1
}

// matchParen returns the position of the parenthesis matching the one at open.
func matchParen(source string, open int) int {
	depth := 0
	for i := open; i < len(source); {
		if next, ok := skipNonCode(source, i); ok {
			i = next
			continue
		}
		switch source[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return -1
}

// readIdent reads an identifier starting at i.
func readIdent(source string, i int) (string, int) {
	end := i
	for end < len(source) && isIdentPart(source[end]) {
		end++
	}
	return source[i:end], end
}

// skipWhitespaceAndComments advances past whitespace and comments.
func skipWhitespaceAndComments(source string, i int) int {
	for i < len(source) {
		if isSpace(source[i]) {
			i++
			continue
		}
		if next, ok := skipNonCode(source, i); ok && (source[i] == '/') {
			i = next
			continue
		}
		break
	}
	return i
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func isIdentStart(b byte) bool {
	return b == '_' || b == '$' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z')
}

func isIdentPart(b byte) bool {
	return isIdentStart(b) || (b >= '0' && b <= '9')
}
