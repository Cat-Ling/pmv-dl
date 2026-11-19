package extractor

import (
	"context"
	"net/http"
	"net/url"
	"sync"

	"pmv-dl/internal/common"
)

type Capability uint32

const (
	CapVideo Capability = 1 << iota
	CapShort
	CapPlaylist
	CapGallery
	CapImage
)

type ResultType string

const (
	ResultTypeVideo    ResultType = "video"
	ResultTypeImage    ResultType = "image"
	ResultTypePlaylist ResultType = "playlist"
)

type VideoResult struct {
	Title     string
	PageURL   string
	StreamURL string
	Referer   string
	Headers   map[string]string
}

type ImageResult struct {
	Title     string
	PageURL   string
	ImageURLs []string
	Referer   string
	Headers   map[string]string
}

type PlaylistEntry struct {
	Type      ResultType
	Title     string
	URL       string
	ImageURLs []string
	StreamURL string
}

type PlaylistResult struct {
	Title   string
	PageURL string
	Entries []PlaylistEntry
}

type Result struct {
	Type     ResultType
	Video    *VideoResult
	Image    *ImageResult
	Playlist *PlaylistResult
}

// Extractor defines the interface that all site plugins must implement.
type Extractor interface {
	Name() string
	Description() string
	Capabilities() Capability
	Match(u *url.URL) bool
	Extract(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error)
}

var (
	registryMu sync.RWMutex
	registry   []Extractor
)

func Register(e Extractor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = append(registry, e)
}

func Find(u *url.URL) Extractor {
	registryMu.RLock()
	defer registryMu.RUnlock()

	var generic Extractor
	for _, e := range registry {
		if e.Name() == "generic" {
			generic = e
			continue
		}
		if e.Match(u) {
			return e
		}
	}
	if generic != nil && generic.Match(u) {
		return generic
	}
	return nil
}

func All() []Extractor {
	registryMu.RLock()
	defer registryMu.RUnlock()
	copied := make([]Extractor, len(registry))
	copy(copied, registry)
	return copied
}
