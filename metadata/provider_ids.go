package metadata

import "strings"

const CapabilityID = "manga-metadata"

func ProviderIDsFromMatch(match Match) map[string]string {
	ids := make(map[string]string)

	provider := strings.TrimSpace(match.Provider)
	providerID := strings.TrimSpace(match.ProviderID)
	if provider != "" && providerID != "" {
		ids[provider] = providerID
		ids[CapabilityID] = provider + ":" + providerID
	}

	for key, value := range match.ExternalIDs {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" || key == provider {
			continue
		}
		if _, exists := ids[key]; !exists {
			ids[key] = value
		}
	}

	return ids
}

func ParseCapabilityProviderID(value string) (string, string) {
	source, id, ok := strings.Cut(strings.TrimSpace(value), ":")
	if !ok {
		return "", ""
	}

	source = strings.TrimSpace(source)
	id = strings.TrimSpace(id)
	if source == "" || id == "" {
		return "", ""
	}

	return source, id
}
