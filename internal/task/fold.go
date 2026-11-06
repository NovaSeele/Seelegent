package task

import (
	"strings"
	"unicode"
)

// foldBase maps the lower-case accented Latin letters that titles use (Vietnamese in full, and the common Western
// European ones) to their base letter. The standard library has no Unicode decomposition, and no dependency is
// added for it.
var foldBase = func() *strings.Replacer {
	groups := map[string]string{
		"a": "àáảãạăằắẳẵặâầấẩẫậäåāąǎ",
		"c": "çćčĉċ",
		"d": "đď",
		"e": "èéẻẽẹêềếểễệëēęěė",
		"g": "ğĝġģ",
		"i": "ìíỉĩịîïīįı",
		"l": "łľĺļ",
		"n": "ñńňņ",
		"o": "òóỏõọôồốổỗộơờớởỡợöøōőǒ",
		"r": "řŕ",
		"s": "śšşŝș",
		"t": "ťţț",
		"u": "ùúủũụưừứửữựûüūůűųǔ",
		"y": "ỳýỷỹỵÿŷ",
		"z": "źżž",
	}
	var pairs []string
	for base, accented := range groups {
		for _, r := range accented {
			pairs = append(pairs, string(r), base)
		}
	}
	return strings.NewReplacer(pairs...)
}()

// foldText lower-cases s and removes the diacritics of Latin letters, for case- and accent-insensitive search.
func foldText(s string) string {
	return foldBase.Replace(strings.Map(unicode.ToLower, s))
}
