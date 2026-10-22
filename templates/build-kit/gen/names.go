package main

import (
	"go/token"
	"strings"
	"unicode"
)

// initialisms are written in capitals in Go identifiers.
var initialisms = map[string]string{
	"id": "ID", "uuid": "UUID", "url": "URL", "uri": "URI", "api": "API",
	"http": "HTTP", "json": "JSON", "sku": "SKU", "ip": "IP", "html": "HTML",
	"vat": "VAT", "iban": "IBAN",
}

// words splits a title or field name into words: on anything that is not a
// letter or digit, and where a lower-case letter or digit meets an upper-case
// one ("cartId" → "cart", "Id").
func words(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = cur[:0]
		}
	}
	runes := []rune(s)
	for i, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if unicode.IsUpper(r) && i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])) {
			flush()
		}
		cur = append(cur, r)
	}
	flush()
	return out
}

// exportedName turns a title or field name into an exported Go identifier:
// "Add Item" → "AddItem", "cartId" → "CartID".
func exportedName(s string) string {
	var b strings.Builder
	for _, w := range words(s) {
		lw := strings.ToLower(w)
		if up, ok := initialisms[lw]; ok {
			b.WriteString(up)
			continue
		}
		r := []rune(lw)
		r[0] = unicode.ToUpper(r[0])
		b.WriteString(string(r))
	}
	name := b.String()
	if name == "" {
		return "X"
	}
	if unicode.IsDigit([]rune(name)[0]) {
		name = "X" + name
	}
	return name
}

// unexportedName is exportedName with a lower-case start: "CartID" →
// "cartID", "ID" → "id".
func unexportedName(s string) string {
	name := exportedName(s)
	ws := words(s)
	if len(ws) > 0 {
		if up, ok := initialisms[strings.ToLower(ws[0])]; ok && strings.HasPrefix(name, up) {
			name = strings.ToLower(up) + name[len(up):]
			return safeIdent(name)
		}
	}
	r := []rune(name)
	r[0] = unicode.ToLower(r[0])
	return safeIdent(string(r))
}

func safeIdent(s string) string {
	if token.IsKeyword(s) {
		return s + "_"
	}
	return s
}

// folderName is how Event Modelers names a slice folder: the title in
// lower case, letters and digits only ("Add Item" → "additem").
func folderName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// packageName turns a slice folder into a Go package name.
func packageName(folder string) string {
	name := folderName(folder)
	if name == "" {
		return "slice"
	}
	if unicode.IsDigit([]rune(name)[0]) || token.IsKeyword(name) {
		name = "s" + name
	}
	return name
}

// kebab turns a title into a lower-case, dash-separated name for routes and
// projection names: "Cart Items" → "cart-items".
func kebab(s string) string {
	ws := words(s)
	for i, w := range ws {
		ws[i] = strings.ToLower(w)
	}
	return strings.Join(ws, "-")
}

// sameName reports whether two model names denote the same field or element,
// ignoring case and separators.
func sameName(a, b string) bool {
	return a != "" && b != "" && folderName(a) == folderName(b)
}
