package provider

import "testing"

func mbSeries(id int, title string, secondary ...string) mangaBakaSeries {
	s := mangaBakaSeries{ID: id, Title: title, Type: "manga"}
	if len(secondary) > 0 {
		group := make([]mangaBakaSecondaryTitle, 0, len(secondary))
		for _, t := range secondary {
			group = append(group, mangaBakaSecondaryTitle{Type: "alternative", Title: t})
		}
		s.SecondaryTitles = map[string][]mangaBakaSecondaryTitle{"en": group}
	}
	return s
}

func TestPickConfidentMangaBakaMatchExact(t *testing.T) {
	cands := []mangaBakaSeries{
		mbSeries(1, "Chainsaw Man"),
		mbSeries(2, "Fire Punch"),
	}
	got := pickConfidentMangaBakaMatch("chainsaw man", cands)
	if got == nil || got.ID != 1 {
		t.Fatalf("got %+v, want id 1", got)
	}
}

func TestPickConfidentMangaBakaMatchSecondaryTitle(t *testing.T) {
	cands := []mangaBakaSeries{mbSeries(1, "Kanojo, Okarishimasu", "Rent-A-Girlfriend")}
	got := pickConfidentMangaBakaMatch("Rent-A-Girlfriend", cands)
	if got == nil || got.ID != 1 {
		t.Fatalf("secondary-title match failed: %+v", got)
	}
}

func TestPickConfidentMangaBakaMatchAmbiguousIsNil(t *testing.T) {
	cands := []mangaBakaSeries{
		mbSeries(1, "Bleach"),
		mbSeries(2, "Bleach"),
	}
	if got := pickConfidentMangaBakaMatch("bleach", cands); got != nil {
		t.Fatalf("ambiguous exact match must be nil, got %+v", got)
	}
}

func TestPickConfidentMangaBakaMatchNoMatch(t *testing.T) {
	cands := []mangaBakaSeries{mbSeries(1, "Naruto")}
	if got := pickConfidentMangaBakaMatch("One Piece", cands); got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
}
