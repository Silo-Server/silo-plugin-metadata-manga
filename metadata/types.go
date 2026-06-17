package metadata

type SearchQuery struct {
	Title       string
	Authors     []string
	Year        int
	ContentType string
	ProviderIDs map[string]string
	Language    string
}

type Match struct {
	Provider       string
	ProviderID     string
	Title          string
	Subtitle       string
	Authors        []string
	Description    string
	Publisher      string
	PublishYear    int
	ISBN           string
	Genres         []string
	CoverURL       string
	BannerURL      string
	Status         string
	Language       string
	ContentRating  string
	ExternalIDs    map[string]string
	PageCount      int
	SeriesName     string
	SeriesPosition string
}
