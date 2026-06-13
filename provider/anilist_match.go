package provider

import (
	"regexp"
	"strings"
	"unicode"
)

// normalizeTitle lowercases and strips all non-alphanumeric runes so that
// punctuation/spacing differences ("One-Punch Man" vs "one punch man") match.
// "&" reads as "and" and leading zeros are dropped from digit runs so
// "Part 01" ≡ "Part 1".
func normalizeTitle(s string) string {
	s = strings.ReplaceAll(strings.ToLower(s), "&", "and")
	var b strings.Builder
	zeroRun := false // pending zeros at the start of the current digit run
	inDigits := false
	for _, r := range s {
		switch {
		case unicode.IsDigit(r):
			if r == '0' && !inDigits {
				zeroRun = true // hold; written only if the whole run is zeros
				continue
			}
			inDigits = true
			zeroRun = false
			b.WriteRune(r)
		case unicode.IsLetter(r):
			if zeroRun {
				b.WriteRune('0') // all-zero run ("Chapter 0"): keep one zero
			}
			zeroRun = false
			inDigits = false
			b.WriteRune(r)
		default:
			// Non-alphanumerics end a digit run without emitting anything.
			if zeroRun {
				b.WriteRune('0')
				zeroRun = false
			}
			inDigits = false
		}
	}
	if zeroRun {
		b.WriteRune('0')
	}
	return b.String()
}

// partRunPattern matches "part <n>" release-numbering runs for the
// part-blind comparison tier.
var partRunPattern = regexp.MustCompile(`(?i)\bpart\s*(\d+)\b`)

func normalizePartBlind(s string) string {
	return normalizeTitle(partRunPattern.ReplaceAllString(s, " "))
}

// partNumber returns the first "part <n>" number in s, or "" when none. Used
// to reject a part-blind match where both sides name a different explicit part
// (so "X Part 3" cannot match "X Part 7" just because they share a stem).
func partNumber(s string) string {
	if m := partRunPattern.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// suffixMinQueryLen gates the suffix match tier: the normalized query must be
// long enough for an ends-with match to be trusted.
const suffixMinQueryLen = 8
