package backend

import (
	"strings"
	"sync/atomic"
)

var currentLanguage atomic.Value

func init() {
	currentLanguage.Store("en")
}

func normalizeLanguage(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "zh", "zh-cn", "cn", "chinese":
		return "zh"
	default:
		return "en"
	}
}

func setCurrentLanguage(language string) string {
	normalized := normalizeLanguage(language)
	currentLanguage.Store(normalized)
	return normalized
}

func getCurrentLanguage() string {
	if value, ok := currentLanguage.Load().(string); ok && value != "" {
		return value
	}
	return "en"
}

func useChinese() bool {
	return getCurrentLanguage() == "zh"
}

// SetLanguage changes the language used for names returned by the Go backend.
// The frontend stores the preference and calls this method before mounting.
func (a *App) SetLanguage(language string) string {
	return setCurrentLanguage(language)
}

func (a *App) GetLanguage() string {
	return getCurrentLanguage()
}
