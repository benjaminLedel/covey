package normalise

import (
	"strings"
	"unicode"
)

// Pictographs and symbols a voice has no word for. Go's unicode package
// has no Extended_Pictographic table, so the ranges are here: those of
// Unicode's emoji-data.txt, widened to whole blocks where a block holds
// nothing speakable (dingbats, arrows, box drawing, geometric shapes).
var silentRanges = [][2]rune{
	{0x00A9, 0x00A9}, {0x00AE, 0x00AE}, // © ®
	{0x203C, 0x203C}, {0x2049, 0x2049}, // ‼ ⁉
	{0x2122, 0x2122}, {0x2139, 0x2139}, // ™ ℹ
	{0x2300, 0x23FF}, // miscellaneous technical: ⌚ ⏰ ⏩
	{0x2460, 0x24FF}, // enclosed alphanumerics: Ⓜ ①
	{0x2500, 0x25FF}, // box drawing, blocks, geometric shapes: ▪ ▶ ◀
	{0x2600, 0x27BF}, // miscellaneous symbols, dingbats: ☀ ✅ ❤ ➡
	{0x2900, 0x297F}, // supplemental arrows: ⤴
	{0x2B00, 0x2BFF}, // miscellaneous symbols and arrows: ⬅ ⭐ ⭕
	{0x3030, 0x3030}, {0x303D, 0x303D}, {0x3297, 0x3297}, {0x3299, 0x3299},
	{0xE000, 0xF8FF},   // private use: icon fonts
	{0x1F000, 0x1FAFF}, // mahjong … symbols and pictographs extended-A
	{0x1FC00, 0x1FFFD}, // reserved for future pictographs
}

// isSilent says whether r is dropped. Regional indicators (flags) and skin
// tones fall inside 1F000–1FAFF.
func isSilent(r rune) bool {
	if r < 0xA9 {
		return false
	}
	for _, g := range silentRanges {
		if r < g[0] {
			return false
		}
		if r <= g[1] {
			return true
		}
	}
	return false
}

// isEmojiJoiner: the marks that only shape an emoji — variation selectors,
// the keycap, tags (subdivision flags).
func isEmojiJoiner(r rune) bool {
	return r == 0xFE0E || r == 0xFE0F || r == 0x20E3 || (r >= 0xE0000 && r <= 0xE007F)
}

// dropPictographs removes emojis with everything that forms them: modifiers,
// zero-width joiners inside a sequence, flags, keycaps ("1️⃣" goes whole).
// Arrows become a pause. Linear in the text.
func dropPictographs(s string) string {
	rs := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r >= 0x2190 && r <= 0x21FF: // arrows: "A → B" is "A, B"
			b.WriteString(", ")
		case isSilent(r) || isEmojiJoiner(r):
			b.WriteRune(' ')
		case r == 0x200D: // zero-width joiner: kept only between letters
			if i > 0 && i+1 < len(rs) && unicode.IsLetter(rs[i-1]) && unicode.IsLetter(rs[i+1]) {
				b.WriteRune(r)
			}
		case (r >= '0' && r <= '9' || r == '#' || r == '*') && keycapAt(rs, i+1):
			// The base of a keycap: "1️⃣" is a picture, not a number.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// keycapAt: a keycap mark follows at i, with or without a variation selector.
func keycapAt(rs []rune, i int) bool {
	if i < len(rs) && rs[i] == 0xFE0F {
		i++
	}
	return i < len(rs) && rs[i] == 0x20E3
}
