package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/Silo-Server/silo-plugin-manga-metadata/metadata"
	"github.com/Silo-Server/silo-plugin-manga-metadata/provider"
	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	publicmanifest "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"
	"google.golang.org/protobuf/types/known/structpb"
)

// version is set at build time via -ldflags "-X main.version=...".
var version string

const capabilityID = "manga-metadata"

type runtimeServer struct {
	pluginv1.UnimplementedRuntimeServer

	manifest *pluginv1.PluginManifest
	mu       sync.RWMutex
	state    runtimeState
}

type runtimeState struct {
	provider *provider.Provider
	options  provider.Options
}

type metadataServer struct {
	pluginv1.UnimplementedMetadataProviderServer
	runtime *runtimeServer
}

//go:embed manifest.json
var manifestJSON []byte

func (s *runtimeServer) GetManifest(context.Context, *pluginv1.GetManifestRequest) (*pluginv1.GetManifestResponse, error) {
	return &pluginv1.GetManifestResponse{Manifest: s.manifest}, nil
}

func (s *runtimeServer) Configure(_ context.Context, req *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
	options := providerOptionsFromConfig(req.GetConfig())
	newProvider := provider.NewProviderWithOptions(options)
	newProvider.Start()

	s.mu.Lock()
	old := s.state.provider
	s.state = runtimeState{
		provider: newProvider,
		options:  options,
	}
	s.mu.Unlock()

	// Cancel the superseded provider's background lifecycle (the previous dump
	// download/refresh goroutine) so a reconfigure never orphans it.
	if old != nil {
		_ = old.Close()
	}
	return &pluginv1.ConfigureResponse{}, nil
}

func (s *runtimeServer) stateForRequest() runtimeState {
	s.mu.RLock()
	state := s.state
	s.mu.RUnlock()
	if state.provider == nil {
		state.provider = provider.NewProvider()
	}
	return state
}

func (s *metadataServer) Search(ctx context.Context, req *pluginv1.SearchMetadataRequest) (*pluginv1.SearchMetadataResponse, error) {
	state := s.runtime.stateForRequest()

	matches, err := state.provider.Search(ctx, metadata.SearchQuery{
		Title:       req.GetQuery(),
		Year:        int(req.GetYear()),
		ContentType: req.GetItemType(),
		ProviderIDs: stringMapFromStruct(req.GetProviderIds()),
		Language:    firstText(req.GetLanguage(), state.options.DefaultRegion),
	})
	if err != nil {
		return nil, err
	}

	response := &pluginv1.SearchMetadataResponse{
		Results: make([]*pluginv1.ProviderSearchResult, 0, len(matches)),
	}
	for _, match := range matches {
		result, err := providerSearchResultFromMatch(match, req.GetItemType())
		if err != nil {
			return nil, err
		}
		if result != nil {
			response.Results = append(response.Results, result)
		}
	}
	return response, nil
}

func (s *metadataServer) GetMetadata(ctx context.Context, req *pluginv1.GetMetadataRequest) (*pluginv1.GetMetadataResponse, error) {
	state := s.runtime.stateForRequest()

	match, err := state.provider.Fetch(ctx, metadata.SearchQuery{
		ProviderIDs: providerIDsFromProto(req.GetProviderIds(), capabilityID, req.GetProviderId()),
		ContentType: req.GetItemType(),
		Language:    firstText(req.GetLanguage(), state.options.DefaultRegion),
	})
	if err != nil {
		return nil, err
	}
	if match == nil {
		return &pluginv1.GetMetadataResponse{}, nil
	}

	item, err := metadataItemFromMatch(*match, req.GetItemType())
	if err != nil {
		return nil, err
	}
	return &pluginv1.GetMetadataResponse{Item: item}, nil
}

func providerSearchResultFromMatch(match metadata.Match, itemType string) (*pluginv1.ProviderSearchResult, error) {
	providerIDs, err := stringStruct(metadata.ProviderIDsFromMatch(match))
	if err != nil {
		return nil, err
	}

	return &pluginv1.ProviderSearchResult{
		ProviderId:    primaryProviderID(match),
		Title:         strings.TrimSpace(match.Title),
		OriginalTitle: strings.TrimSpace(match.Title),
		Year:          int32(match.PublishYear),
		Overview:      strings.TrimSpace(match.Description),
		ImageUrl:      strings.TrimSpace(match.CoverURL),
		ItemType:      strings.TrimSpace(itemType),
		ProviderIds:   providerIDs,
	}, nil
}

func metadataItemFromMatch(match metadata.Match, itemType string) (*pluginv1.MetadataItem, error) {
	providerIDs, err := stringStruct(metadata.ProviderIDsFromMatch(match))
	if err != nil {
		return nil, err
	}

	return &pluginv1.MetadataItem{
		ProviderId:    primaryProviderID(match),
		Title:         strings.TrimSpace(match.Title),
		OriginalTitle: strings.TrimSpace(match.Title),
		Year:          int32(match.PublishYear),
		Overview:      strings.TrimSpace(match.Description),
		Genres:        genresFromMatch(match),
		ProviderIds:   providerIDs,
		Metadata:      metadataStruct(match),
		Studios:       publisherStudio(match.Publisher),
		PosterPath:    strings.TrimSpace(match.CoverURL),
		BackdropPath:  strings.TrimSpace(match.BannerURL),
		Status:        strings.TrimSpace(match.Status),
		ContentRating: strings.TrimSpace(match.ContentRating),
		People:        peopleFromMatch(match),
		ItemType:      strings.TrimSpace(itemType),
	}, nil
}

func primaryProviderID(match metadata.Match) string {
	provider := strings.TrimSpace(match.Provider)
	providerID := strings.TrimSpace(match.ProviderID)
	if provider != "" && providerID != "" {
		return provider + ":" + providerID
	}
	return ""
}

func stringMapFromStruct(value *structpb.Struct) map[string]string {
	result := make(map[string]string)
	if value == nil {
		return result
	}
	for key, raw := range value.AsMap() {
		text, ok := raw.(string)
		if ok && text != "" {
			result[key] = text
		}
	}
	return result
}

func providerOptionsFromConfig(entries []*pluginv1.ConfigEntry) provider.Options {
	var options provider.Options
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		value := configEntryString(entry.GetValue())
		switch strings.TrimSpace(entry.GetKey()) {
		case "enabled_sources":
			options.EnabledSources = append(options.EnabledSources, value)
		case "default_region":
			options.DefaultRegion = value
		case "enable_local_dump":
			options.EnableLocalDump = configEntryBoolDefault(entry.GetValue(), false)
		case "dump_path":
			options.DumpPath = value
		case "dump_refresh_hours":
			if n, ok := configEntryNumber(entry.GetValue()); ok && n > 0 {
				options.DumpRefreshHours = n
			}
		case "enable_anilist_banners":
			// Stored as "enable"; Options carries the inverse so the zero value
			// (banners on) matches the default. Missing entry => default true.
			options.DisableAniListBanners = !configEntryBoolDefault(entry.GetValue(), true)
		}
	}
	return options
}

func configEntryString(value *structpb.Struct) string {
	if value == nil {
		return ""
	}
	fields := value.GetFields()
	for _, key := range []string{"value", "string", "text"} {
		if raw := fields[key]; raw != nil {
			return strings.TrimSpace(raw.GetStringValue())
		}
	}
	for _, raw := range fields {
		if text := strings.TrimSpace(raw.GetStringValue()); text != "" {
			return text
		}
	}
	return ""
}

func configEntryBoolDefault(value *structpb.Struct, def bool) bool {
	if value == nil {
		return def
	}
	raw, ok := value.GetFields()["value"]
	if !ok || raw == nil {
		return def
	}
	switch raw.GetKind().(type) {
	case *structpb.Value_BoolValue:
		return raw.GetBoolValue()
	case *structpb.Value_NumberValue:
		return raw.GetNumberValue() != 0
	case *structpb.Value_StringValue:
		switch strings.ToLower(strings.TrimSpace(raw.GetStringValue())) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		default:
			// Empty or unrecognized strings keep the caller's default rather
			// than silently flipping the setting off.
			return def
		}
	}
	return def
}

func configEntryNumber(value *structpb.Struct) (int, bool) {
	if value == nil {
		return 0, false
	}
	raw, ok := value.GetFields()["value"]
	if !ok || raw == nil {
		return 0, false
	}
	switch raw.GetKind().(type) {
	case *structpb.Value_NumberValue:
		return int(raw.GetNumberValue()), true
	case *structpb.Value_StringValue:
		if n, err := strconv.Atoi(strings.TrimSpace(raw.GetStringValue())); err == nil {
			return n, true
		}
	}
	return 0, false
}

func firstText(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func providerIDsFromProto(value *structpb.Struct, capabilityID string, fallbackID string) map[string]string {
	result := stringMapFromStruct(value)
	if fallbackID != "" && result[capabilityID] == "" {
		result[capabilityID] = fallbackID
	}
	return result
}

func stringStruct(value map[string]string) (*structpb.Struct, error) {
	fields := make(map[string]any)
	for key, val := range value {
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key == "" || val == "" {
			continue
		}
		fields[key] = val
	}
	return structpb.NewStruct(fields)
}

func metadataStruct(match metadata.Match) *structpb.Struct {
	fields := make(map[string]*structpb.Value)
	addString := func(key, value string) {
		if value = strings.TrimSpace(value); value != "" {
			fields[key] = structpb.NewStringValue(value)
		}
	}

	addString("language", match.Language)
	addString("series_name", match.SeriesName)
	addString("series_position", match.SeriesPosition)

	return &structpb.Struct{Fields: fields}
}

func publisherStudio(publisher string) []string {
	publisher = strings.TrimSpace(publisher)
	if publisher == "" {
		return nil
	}
	return []string{publisher}
}

func genresFromMatch(match metadata.Match) []string {
	genres := make([]string, 0, len(match.Genres))
	for _, genre := range match.Genres {
		genre = strings.TrimSpace(genre)
		if genre == "" {
			continue
		}
		genres = append(genres, genre)
	}
	if len(genres) == 0 {
		return nil
	}
	return genres
}

func peopleFromMatch(match metadata.Match) []*pluginv1.PersonRecord {
	people := make([]*pluginv1.PersonRecord, 0, len(match.Authors))
	for _, author := range match.Authors {
		author = strings.TrimSpace(author)
		if author == "" {
			continue
		}
		people = append(people, &pluginv1.PersonRecord{
			Name:      author,
			Kind:      "author",
			SortOrder: int32(len(people)),
		})
	}
	return people
}

func main() {
	manifest, err := loadManifest()
	if err != nil {
		panic(err)
	}

	rs := &runtimeServer{
		manifest: manifest,
		state:    runtimeState{provider: provider.NewProvider()},
	}

	runtime.Serve(runtime.ServeConfig{
		Servers: runtime.CapabilityServers{
			Runtime:          rs,
			MetadataProvider: &metadataServer{runtime: rs},
		},
	})
}

func loadManifest() (*pluginv1.PluginManifest, error) {
	manifest, err := publicmanifest.Load(manifestJSON)
	if err != nil {
		return nil, fmt.Errorf("load embedded manifest: %w", err)
	}

	if version != "" {
		manifest.Version = version
	}

	executablePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable path: %w", err)
	}
	binaryData, err := os.ReadFile(executablePath)
	if err != nil {
		return nil, fmt.Errorf("read executable %q: %w", executablePath, err)
	}
	checksum := sha256.Sum256(binaryData)
	manifest.Checksum = hex.EncodeToString(checksum[:])

	return manifest, nil
}
