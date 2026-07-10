package provider

// MangaDex API client: search and fetch-by-id against api.mangadex.org.
// MangaDex requires a descriptive User-Agent and allows ~5 req/s per IP;
// a 1 req/s limiter keeps this plugin far inside that budget.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

const (
	mangaDexEndpoint  = "https://api.mangadex.org"
	mangaDexCoverBase = "https://uploads.mangadex.org/covers"
	mangaDexUserAgent = "silo-plugin-metadata-manga (https://github.com/Silo-Server/silo-plugin-metadata-manga)"
)

var mangaDexLimiter = rate.NewLimiter(rate.Every(time.Second), 1)

// mangaDexManga is the subset of a MangaDex manga entity the matcher and
// mapper consume. Relationships carry cover/author/artist attributes when the
// request includes them.
type mangaDexManga struct {
	ID         string `json:"id"`
	Attributes struct {
		Title       map[string]string   `json:"title"`
		AltTitles   []map[string]string `json:"altTitles"`
		Description map[string]string   `json:"description"`
		Year        int                 `json:"year"`
		Status      string              `json:"status"`
		Tags        []struct {
			Attributes struct {
				Name  map[string]string `json:"name"`
				Group string            `json:"group"`
			} `json:"attributes"`
		} `json:"tags"`
	} `json:"attributes"`
	Relationships []struct {
		Type       string `json:"type"`
		Attributes *struct {
			Name     string `json:"name"`
			FileName string `json:"fileName"`
		} `json:"attributes"`
	} `json:"relationships"`
}

// titleValues returns every title string across languages plus alt titles,
// for exact-after-normalize matching.
func (m *mangaDexManga) titleValues() []string {
	values := make([]string, 0, 4+len(m.Attributes.AltTitles))
	for _, title := range m.Attributes.Title {
		values = append(values, title)
	}
	for _, alt := range m.Attributes.AltTitles {
		for _, title := range alt {
			values = append(values, title)
		}
	}
	return values
}

func (m *mangaDexManga) coverURL() string {
	for _, rel := range m.Relationships {
		if rel.Type == "cover_art" && rel.Attributes != nil && rel.Attributes.FileName != "" {
			return fmt.Sprintf("%s/%s/%s", mangaDexCoverBase, m.ID, rel.Attributes.FileName)
		}
	}
	return ""
}

func (m *mangaDexManga) people() []string {
	var authors, artists []string
	seen := map[string]bool{}
	add := func(dst *[]string, name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		*dst = append(*dst, name)
	}
	for _, rel := range m.Relationships {
		if rel.Attributes == nil {
			continue
		}
		switch rel.Type {
		case "author":
			add(&authors, rel.Attributes.Name)
		case "artist":
			add(&artists, rel.Attributes.Name)
		}
	}
	return append(authors, artists...)
}

func (m *mangaDexManga) genres() []string {
	genres := make([]string, 0, 4)
	for _, tag := range m.Attributes.Tags {
		if tag.Attributes.Group != "genre" {
			continue
		}
		if name := tag.Attributes.Name["en"]; name != "" {
			genres = append(genres, name)
		}
	}
	return genres
}

// mangaDexIncludes are the query params shared by search and by-id fetches:
// cover/author/artist relationship attributes and the full content-rating
// range short of pornographic (the MangaDex default silently hides erotica).
func mangaDexIncludes(params url.Values) {
	params.Add("includes[]", "cover_art")
	params.Add("includes[]", "author")
	params.Add("includes[]", "artist")
}

func mangaDexContentRatings(params url.Values) {
	params.Add("contentRating[]", "safe")
	params.Add("contentRating[]", "suggestive")
	params.Add("contentRating[]", "erotica")
}

// doMangaDexGet performs one rate-limited GET, retrying once on HTTP 429
// honoring Retry-After (default 2s, cap 10s) — same policy as AniList.
func doMangaDexGet(ctx context.Context, client *http.Client, requestURL string) ([]byte, error) {
	attempt := func() (raw []byte, retryDelay time.Duration, err error) {
		if waitErr := mangaDexLimiter.Wait(ctx); waitErr != nil {
			return nil, 0, waitErr
		}
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if reqErr != nil {
			return nil, 0, reqErr
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", mangaDexUserAgent)
		resp, doErr := client.Do(req)
		if doErr != nil {
			return nil, 0, fmt.Errorf("mangadex: request: %w", doErr)
		}
		defer resp.Body.Close()
		raw, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))

		if resp.StatusCode == http.StatusTooManyRequests {
			d := 2 * time.Second
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, parseErr := strconv.Atoi(ra); parseErr == nil {
					d = time.Duration(secs) * time.Second
				}
			}
			if d > 10*time.Second {
				d = 10 * time.Second
			}
			return nil, d, fmt.Errorf("mangadex: transient HTTP %d", resp.StatusCode)
		}
		if resp.StatusCode >= 500 {
			return nil, -1, fmt.Errorf("mangadex: transient HTTP %d", resp.StatusCode)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, -1, fmt.Errorf("mangadex: HTTP %d", resp.StatusCode)
		}
		return raw, -1, nil
	}

	raw, retryDelay, err := attempt()
	if err != nil && retryDelay >= 0 {
		if retryDelay > 0 {
			select {
			case <-time.After(retryDelay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		raw, _, err = attempt()
	}
	return raw, err
}

func searchMangaDex(ctx context.Context, client *http.Client, endpoint, title string) ([]mangaDexManga, error) {
	params := url.Values{}
	params.Set("title", title)
	params.Set("limit", "10")
	params.Set("order[relevance]", "desc")
	mangaDexIncludes(params)
	mangaDexContentRatings(params)

	raw, err := doMangaDexGet(ctx, client, endpoint+"/manga?"+params.Encode())
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result string `json:"result"`
		Errors []struct{ Detail string }
		Data   []mangaDexManga `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("mangadex: decode: %w", err)
	}
	if resp.Result != "ok" {
		return nil, fmt.Errorf("mangadex: result %q", resp.Result)
	}
	return resp.Data, nil
}

func fetchMangaDexByID(ctx context.Context, client *http.Client, endpoint, id string) (*mangaDexManga, error) {
	params := url.Values{}
	mangaDexIncludes(params)

	raw, err := doMangaDexGet(ctx, client, endpoint+"/manga/"+url.PathEscape(id)+"?"+params.Encode())
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result string        `json:"result"`
		Data   mangaDexManga `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("mangadex: decode: %w", err)
	}
	if resp.Result != "ok" || resp.Data.ID == "" {
		return nil, nil
	}
	return &resp.Data, nil
}
