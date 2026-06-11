package provider

import "testing"

const sampleAniListJSON = `{"data":{"Page":{"media":[
  {"id":98257,"title":{"romaji":"One Punch-Man","english":"One-Punch Man","native":"ワンパンマン"},
   "coverImage":{"extraLarge":"https://img/op.jpg"},"description":"A hero <b>for fun</b>.",
   "status":"RELEASING","genres":["Action","Comedy"],"format":"MANGA","startDate":{"year":2012},
   "staff":{"edges":[{"role":"Story","node":{"name":{"full":"ONE"}}},{"role":"Art","node":{"name":{"full":"Yusuke Murata"}}}]}}
]}}}`

func TestParseAniListSearch(t *testing.T) {
	media, err := parseAniListSearch([]byte(sampleAniListJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(media) != 1 {
		t.Fatalf("want 1 media, got %d", len(media))
	}
	m := media[0]
	if m.ID != 98257 || m.Title.English != "One-Punch Man" || m.Format != "MANGA" {
		t.Fatalf("bad parse: %+v", m)
	}
	if m.Status != "RELEASING" || len(m.Genres) != 2 {
		t.Fatalf("bad fields: %+v", m)
	}
}
