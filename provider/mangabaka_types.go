package provider

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-plugin-manga-metadata/metadata"
)

const mangaBakaProviderID = "mangabaka"

// mangaBakaSeries is the subset of a MangaBaka series record this plugin maps,
// shared by the live REST backend and the JSONL dump backend so both decode
// into one struct and map through one function.
type mangaBakaSeries struct {
	ID              int                                  `json:"id"`
	State           string                               `json:"state"`
	Title           string                               `json:"title"`
	NativeTitle     string                               `json:"native_title"`
	RomanizedTitle  string                               `json:"romanized_title"`
	SecondaryTitles map[string][]mangaBakaSecondaryTitle `json:"secondary_titles"`
	Cover           mangaBakaCover                       `json:"cover"`
	Authors         []string                             `json:"authors"`
	Artists         []string                             `json:"artists"`
	Description     string                               `json:"description"`
	Year            int                                  `json:"year"`
	Status          string                               `json:"status"`
	ContentRating   string                               `json:"content_rating"`
	Type            string                               `json:"type"`
	Rating          float64                              `json:"rating"`
	Genres          []string                             `json:"genres"`
	Publishers      []string                             `json:"publishers"`
	Source          map[string]mangaBakaSourceRef        `json:"source"`
}

type mangaBakaSecondaryTitle struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	Note  string `json:"note"`
}

type mangaBakaCover struct {
	Raw struct {
		URL string `json:"url"`
	} `json:"raw"`
}

// mangaBakaSourceRef holds one upstream provider reference. ID is raw because
// MangaBaka returns numeric ids (anilist, my_anime_list) and string ids
// (manga_updates, anime_planet) in the same map.
type mangaBakaSourceRef struct {
	ID json.RawMessage `json:"id"`
}

func (r mangaBakaSourceRef) idString() string {
	raw := strings.TrimSpace(string(r.ID))
	if raw == "" || raw == "null" {
		return ""
	}
	// MangaBaka returns numeric ids (anilist, my_anime_list) and string ids
	// (manga_updates, anime_planet) in the same map. Decode via JSON so
	// JSON-specific escapes (\/ , \uXXXX) are handled correctly.
	var s string
	if err := json.Unmarshal(r.ID, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if err := json.Unmarshal(r.ID, &n); err == nil {
		return n.String()
	}
	return raw
}

// mangaBakaPeople folds authors then artists into one de-duplicated slice,
// matching how metadata.Match represents people (no separate artist field).
func mangaBakaPeople(s mangaBakaSeries) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(s.Authors)+len(s.Artists))
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, a := range s.Authors {
		add(a)
	}
	for _, a := range s.Artists {
		add(a)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// mangaBakaTitleValues returns every title worth matching against: primary,
// native, romanized, and all secondary titles across languages.
func mangaBakaTitleValues(s mangaBakaSeries) []string {
	values := []string{s.Title, s.NativeTitle, s.RomanizedTitle}
	for _, group := range s.SecondaryTitles {
		for _, t := range group {
			values = append(values, t.Title)
		}
	}
	return values
}

func mangaBakaExternalIDs(s mangaBakaSeries) map[string]string {
	ids := make(map[string]string, len(s.Source))
	for key, ref := range s.Source {
		key = strings.TrimSpace(key)
		id := ref.idString()
		if key == "" || id == "" {
			continue
		}
		ids[key] = id
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

func toMatchFromMangaBaka(s mangaBakaSeries) metadata.Match {
	return metadata.Match{
		Provider:      mangaBakaProviderID,
		ProviderID:    strconv.Itoa(s.ID),
		Title:         strings.TrimSpace(firstNonEmpty(s.Title, s.RomanizedTitle, s.NativeTitle)),
		Description:   cleanDescription(s.Description),
		CoverURL:      strings.TrimSpace(s.Cover.Raw.URL),
		Status:        mangaStatusLabel(s.Status),
		ContentRating: strings.TrimSpace(s.ContentRating),
		Genres:        trimmedNonEmpty(s.Genres),
		Authors:       mangaBakaPeople(s),
		PublishYear:   s.Year,
		Publisher:     firstNonEmpty(s.Publishers...),
		ExternalIDs:   mangaBakaExternalIDs(s),
	}
}

func (s mangaBakaSeries) ProviderIDString() string { return strconv.Itoa(s.ID) }

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func trimmedNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
