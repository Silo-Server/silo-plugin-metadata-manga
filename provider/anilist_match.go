package provider

import (
	"regexp"
	"strings"
	"unicode"
)

var mangaFormats = map[string]bool{"MANGA": true, "MANHWA": true, "MANHUA": true, "ONE_SHOT": true}

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

const (
	// suffixMinQueryLen and suffixMinCoverage gate the suffix tier: the
	// normalized query must be long enough and cover at least half the
	// candidate title for an ends-with match to be trusted.
	suffixMinQueryLen = 8

	// popularityDominanceRatio / popularityDominanceFloor gate the same-format
	// tie-break: the top entry must dwarf every contender AND be popular in
	// absolute terms (re-releases and special-chapter duplicates are tiny next
	// to the real serialization; two genuinely distinct works are not).
	popularityDominanceRatio = 10
	popularityDominanceFloor = 1000
)

// pickConfidentMatch returns the single manga-format candidate that matches
// the query through one of four confidence tiers, strongest first:
//
//  1. exact: a normalized title (romaji/english/native) equals the query;
//  2. synonym: an AniList alternate title equals the query;
//  3. part-blind: title equals the query once "part <n>" runs are removed
//     from both sides (part numbering is release metadata: "JoJo's Bizarre
//     Adventure - Battle Tendency" ≡ "... Part 2: Battle Tendency");
//  4. suffix: a title ends with the query and the query covers at least half
//     of it (localized folder names drop franchise prefixes: "Gurren Lagann"
//     for "Tengen Toppa Gurren Lagann").
//
// The first tier with any contenders decides; weaker tiers are never used to
// rescue a stronger tier's ambiguity. Within a tier the match must be unique
// after tie-breaking (see uniqueMatch); anything still ambiguous stays nil
// rather than guessing.
func pickConfidentMatch(query string, candidates []aniListMedia) *aniListMedia {
	want := normalizeTitle(query)
	if want == "" {
		return nil
	}
	wantPartBlind := normalizePartBlind(query)

	queryPart := partNumber(query)

	var exact, synonym, partBlind, suffix []*aniListMedia
	for i := range candidates {
		c := &candidates[i]
		if !mangaFormats[strings.ToUpper(c.Format)] {
			continue
		}
		titles := []string{c.Title.Romaji, c.Title.English, c.Title.Native}

		matched := false
		for _, title := range titles {
			if normalizeTitle(title) == want {
				exact = append(exact, c)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		for _, syn := range c.Synonyms {
			if normalizeTitle(syn) == want {
				synonym = append(synonym, c)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		for _, title := range titles {
			if normalizePartBlind(title) != wantPartBlind {
				continue
			}
			// Reject when both sides name an explicit but different part, so a
			// "Part 3" folder cannot confidently match a "Part 7" entry.
			if titlePart := partNumber(title); queryPart != "" && titlePart != "" && queryPart != titlePart {
				continue
			}
			partBlind = append(partBlind, c)
			matched = true
			break
		}
		if matched {
			continue
		}
		if len(want) >= suffixMinQueryLen {
			for _, title := range titles {
				normalized := normalizeTitle(title)
				if strings.HasSuffix(normalized, want) && len(want)*2 >= len(normalized) {
					suffix = append(suffix, c)
					break
				}
			}
		}
	}

	for _, tier := range [][]*aniListMedia{exact, synonym, partBlind, suffix} {
		if len(tier) > 0 {
			return uniqueMatch(tier)
		}
	}
	return nil
}

// uniqueMatch reduces a set of equally-confident candidates to a single pick:
// one candidate; or the only MANGA serialization among non-MANGA siblings
// (AniList lists many series twice, as the MANGA and as a same-titled pilot
// ONE_SHOT); or the overwhelmingly most popular contender (duplicate entries
// for re-releases and special chapters are tiny next to the real
// serialization). A near-peer tie is genuine ambiguity and stays nil.
func uniqueMatch(matches []*aniListMedia) *aniListMedia {
	if len(matches) == 0 {
		return nil
	}
	if len(matches) == 1 {
		return matches[0]
	}

	pool := matches
	var manga []*aniListMedia
	for _, m := range matches {
		if strings.ToUpper(m.Format) == "MANGA" {
			manga = append(manga, m)
		}
	}
	if len(manga) == 1 {
		return manga[0]
	}
	if len(manga) > 1 {
		pool = manga
	}

	top, second := (*aniListMedia)(nil), 0
	for _, m := range pool {
		switch {
		case top == nil || m.Popularity > top.Popularity:
			if top != nil && top.Popularity > second {
				second = top.Popularity
			}
			top = m
		case m.Popularity > second:
			second = m.Popularity
		}
	}
	if second < 1 {
		second = 1
	}
	if top != nil && top.Popularity >= popularityDominanceFloor && top.Popularity >= popularityDominanceRatio*second {
		return top
	}
	return nil
}
