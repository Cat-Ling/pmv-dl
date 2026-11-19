package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"pmv-dl/internal/common"
)

var (
	reNoodlePlaylist    = regexp.MustCompile(`(?s)window\.playlist\s*=\s*(\{.+?\});`)
	reNoodleWatchID     = regexp.MustCompile(`/watch/(-?\d+_\d+)`)
	reNoodleDownloadURL = regexp.MustCompile(`downloadUrl=["']([^"']+)["']`)
	reNoodleOGTitle     = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:title["'][^>]+content=["'](.*?)["']\s*(?:/?>|[a-zA-Z_-]+=)`)
	reNoodleTitleTag    = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reNoodleTitleBase   = regexp.MustCompile(`(?is)data-video-title-base=["'](.*?)["']`)
	reNoodleOGImage     = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:image["'][^>]+content=["'](.*?)["']`)
	reNoodleYAVideo     = regexp.MustCompile(`(?is)<meta[^>]+property=["']ya:ovs:content_url["'][^>]+content=["'](.*?)["']`)
	reNoodleOGVideo     = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:video(?::url)?["'][^>]+content=["'](.*?)["']`)
	reNoodleSuffix      = regexp.MustCompile(`(?i)\s*(?:[-|]\s*)?(?:BEST\s+XXX\s+TUBE|Noodle\s*Magazine|NoodleMagazine|Mat6tube|exporntoons|ukdevilz|tyler-brown|BrownPorn|pornjav|porn\s+video\s+on\s+[a-zA-Z0-9_-]+|watch\s+online(?:\s+(?:high|hight)\s+quality\s+video)?).*$`)
	reNoodleWatchOnline = regexp.MustCompile(`(?i)\s*[-|]?\s*watch\s+online\s*$`)
	reNoodleHTMLTags    = regexp.MustCompile(`<[^>]*>`)

	networkMirrors = []string{
		"mat6tube.com",
		"ukdevilz.com",
		"exporntoons.net",
		"noodlemagazine.com",
	}
)

type NoodlePlaylistData struct {
	Image   string         `json:"image"`
	Sources []NoodleSource `json:"sources"`
}

type NoodleSource struct {
	File  string `json:"file"`
	Label string `json:"label"`
	Type  string `json:"type"`
}

type NoodleMagazineExtractor struct{}

func (n *NoodleMagazineExtractor) Name() string {
	return "noodlemagazine"
}

func (n *NoodleMagazineExtractor) Description() string {
	return "NoodleMagazine, Mat6tube, and network proxies/mirrors extractor"
}

func (n *NoodleMagazineExtractor) Capabilities() Capability {
	return CapVideo | CapPlaylist
}

func (n *NoodleMagazineExtractor) Match(u *url.URL) bool {
	host := strings.ToLower(u.Host)
	if colon := strings.Index(host, ":"); colon != -1 {
		host = host[:colon]
	}

	matchesDomain := func(d string) bool {
		return host == d || strings.HasSuffix(host, "."+d)
	}

	return matchesDomain("noodlemagazine.com") ||
		matchesDomain("mat6tube.com") ||
		matchesDomain("exporntoons.net") ||
		matchesDomain("ukdevilz.com") ||
		matchesDomain("tyler-brown.com") ||
		matchesDomain("pornjav.tv") ||
		matchesDomain("nmcorp.video") ||
		matchesDomain("yemoja.xyz")
}

func (n *NoodleMagazineExtractor) Extract(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	urlStr := targetURL.String()
	referer := targetURL.Scheme + "://" + targetURL.Host + "/"
	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, urlStr, referer, common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		// If fetch fails on any network mirror (e.g. Cloudflare challenge or ISP block), try sibling mirrors
		host := strings.ToLower(targetURL.Host)
		for _, mirror := range networkMirrors {
			if strings.EqualFold(mirror, host) {
				continue
			}
			fallbackURLStr := strings.Replace(urlStr, targetURL.Host, mirror, 1)
			if fallbackURL, pErr := url.Parse(fallbackURLStr); pErr == nil {
				fbReferer := fallbackURL.Scheme + "://" + fallbackURL.Host + "/"
				fbBytes, fbFinalURL, fbErr := common.FetchBytesWithRetry(ctx, client, fallbackURL.String(), fbReferer, common.MaxHTMLBytes, common.DefaultTimeout)
				if fbErr == nil {
					if fbFinalURL != nil {
						fallbackURL = fbFinalURL
					}
					return n.ExtractFromHTML(ctx, client, fallbackURL, fbBytes, opts)
				}
			}
		}
		return nil, fmt.Errorf("failed to fetch %s: %w", urlStr, err)
	}
	if finalURL != nil {
		targetURL = finalURL
	}

	return n.ExtractFromHTML(ctx, client, targetURL, bodyBytes, opts)
}

func (n *NoodleMagazineExtractor) ExtractFromHTML(ctx context.Context, client *http.Client, targetURL *url.URL, bodyBytes []byte, opts common.DownloadOptions) (*Result, error) {
	bodyStr := string(bodyBytes)
	path := strings.ToLower(targetURL.Path)

	// Check if this is a listing or search page containing multiple watch links
	isWatchPage := reNoodleWatchID.MatchString(path)
	if !isWatchPage {
		watchMatches := reNoodleWatchID.FindAllStringSubmatch(bodyStr, -1)
		if len(watchMatches) > 0 {
			if opts.NoPlaylist {
				// User wants only the first video from the listing
				firstID := watchMatches[0][1]
				firstURL, _ := url.Parse(fmt.Sprintf("%s://%s/watch/%s", targetURL.Scheme, targetURL.Host, firstID))
				return n.Extract(ctx, client, firstURL, opts)
			}

			// Extract listing playlist
			var entries []PlaylistEntry
			seen := make(map[string]bool)
			for _, m := range watchMatches {
				id := m[1]
				watchURL := fmt.Sprintf("%s://%s/watch/%s", targetURL.Scheme, targetURL.Host, id)
				if !seen[watchURL] {
					seen[watchURL] = true
					entries = append(entries, PlaylistEntry{
						Type:  ResultTypeVideo,
						Title: "video_" + id,
						URL:   watchURL,
					})
				}
			}

			plTitle := n.extractPageTitle(bodyStr, targetURL)
			return &Result{
				Type: ResultTypePlaylist,
				Playlist: &PlaylistResult{
					Title:   common.SanitizeFilename(plTitle),
					PageURL: targetURL.String(),
					Entries: entries,
				},
			}, nil
		}
	}

	// Single video extraction
	title := n.extractVideoTitle(bodyStr, targetURL)
	streamURL := ""

	// 1. Try window.playlist JSON
	if m := reNoodlePlaylist.FindSubmatch(bodyBytes); len(m) > 1 {
		var pl NoodlePlaylistData
		if err := json.Unmarshal(m[1], &pl); err == nil && len(pl.Sources) > 0 {
			chosen := selectNoodleSource(pl.Sources, opts.Quality)
			if chosen.File != "" {
				fileURL, err := url.Parse(chosen.File)
				if err == nil {
					streamURL = targetURL.ResolveReference(fileURL).String()
				} else {
					streamURL = chosen.File
				}
			}
		}
	}

	// 2. Try downloadUrl fallback page if main page didn't have window.playlist
	if streamURL == "" {
		if dlMatch := reNoodleDownloadURL.FindStringSubmatch(bodyStr); len(dlMatch) > 1 {
			dlPath := dlMatch[1]
			dlURL := fmt.Sprintf("%s://%s%s", targetURL.Scheme, targetURL.Host, dlPath)
			dlBytes, _, dlErr := common.FetchBytesWithRetry(ctx, client, dlURL, targetURL.String(), common.MaxHTMLBytes, common.DefaultTimeout)
			if dlErr == nil {
				if m := reNoodlePlaylist.FindSubmatch(dlBytes); len(m) > 1 {
					var pl NoodlePlaylistData
					if err := json.Unmarshal(m[1], &pl); err == nil && len(pl.Sources) > 0 {
						chosen := selectNoodleSource(pl.Sources, opts.Quality)
						if chosen.File != "" {
							fileURL, err := url.Parse(chosen.File)
							if err == nil {
								streamURL = targetURL.ResolveReference(fileURL).String()
							} else {
								streamURL = chosen.File
							}
						}
					}
				}
			}
		}
	}

	// 3. Fallback to meta tags
	if streamURL == "" {
		if m := reNoodleYAVideo.FindStringSubmatch(bodyStr); len(m) > 1 {
			streamURL = m[1]
		} else if m := reNoodleOGVideo.FindStringSubmatch(bodyStr); len(m) > 1 {
			streamURL = m[1]
		}
	}

	if streamURL == "" {
		return nil, fmt.Errorf("could not find video stream in %s", targetURL.String())
	}

	validStream, err := common.ValidateRemoteURL(streamURL)
	if err != nil {
		return nil, fmt.Errorf("invalid stream URL %q: %w", streamURL, err)
	}

	referer := targetURL.Scheme + "://" + targetURL.Host + "/"

	return &Result{
		Type: ResultTypeVideo,
		Video: &VideoResult{
			Title:     title,
			PageURL:   targetURL.String(),
			StreamURL: validStream.String(),
			Referer:   referer,
		},
	}, nil
}

func (n *NoodleMagazineExtractor) extractVideoTitle(bodyStr string, targetURL *url.URL) string {
	raw := ""
	if m := reNoodleTitleBase.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	} else if m := reNoodleOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	} else if m := reNoodleTitleTag.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	}

	raw = reNoodleHTMLTags.ReplaceAllString(raw, "")
	raw = html.UnescapeString(raw)
	raw = strings.TrimSpace(raw)

	// Strip site suffixes and junk
	raw = reNoodleSuffix.ReplaceAllString(raw, "")
	raw = reNoodleWatchOnline.ReplaceAllString(raw, "")
	raw = strings.TrimSpace(raw)

	// If title is a full URL or file path, extract the last component
	if strings.Contains(raw, "://") {
		if u, err := url.Parse(raw); err == nil && u.Path != "" {
			raw = filepath.Base(u.Path)
		} else {
			parts := strings.Split(raw, "/")
			if len(parts) > 1 {
				raw = parts[len(parts)-1]
			}
		}
	} else if strings.Contains(raw, "/") || strings.Contains(raw, "\\") {
		parts := strings.FieldsFunc(raw, func(r rune) bool {
			return r == '/' || r == '\\'
		})
		if len(parts) > 0 {
			raw = parts[len(parts)-1]
		}
	}

	// Clean pipe chains like "Title | tag1 | tag2" -> "Title - tag1 - tag2"
	if strings.Contains(raw, "|") {
		parts := strings.Split(raw, "|")
		var cleanedParts []string
		for _, p := range parts {
			pt := strings.TrimSpace(p)
			if pt != "" {
				cleanedParts = append(cleanedParts, pt)
			}
		}
		raw = strings.Join(cleanedParts, " - ")
	}

	raw = strings.Trim(raw, " .-_")

	if raw == "" {
		if m := reNoodleWatchID.FindStringSubmatch(targetURL.Path); len(m) > 1 {
			host := strings.ToLower(targetURL.Host)
			prefix := "noodlemagazine"
			if strings.Contains(host, "mat6tube") {
				prefix = "mat6tube"
			}
			raw = prefix + "_" + m[1]
		} else {
			raw = "video"
		}
	}

	return common.SanitizeFilename(raw)
}

func (n *NoodleMagazineExtractor) extractPageTitle(bodyStr string, targetURL *url.URL) string {
	title := ""
	if m := reNoodleTitleTag.FindStringSubmatch(bodyStr); len(m) > 1 {
		title = m[1]
	} else if m := reNoodleOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
		title = m[1]
	}

	title = reNoodleHTMLTags.ReplaceAllString(title, "")
	title = html.UnescapeString(title)
	title = strings.TrimSpace(title)
	title = reNoodleSuffix.ReplaceAllString(title, "")
	title = strings.TrimSpace(title)

	if title == "" {
		title = "Noodle Network Playlist"
	}

	return common.SanitizeFilename(title)
}

func selectNoodleSource(sources []NoodleSource, targetQuality string) NoodleSource {
	if len(sources) == 0 {
		return NoodleSource{}
	}
	if len(sources) == 1 {
		return sources[0]
	}

	type scoredSource struct {
		src    NoodleSource
		height int
	}

	var scored []scoredSource
	for _, s := range sources {
		labelStr := strings.TrimSpace(strings.ToLower(s.Label))
		labelStr = strings.TrimSuffix(labelStr, "p")
		h, _ := strconv.Atoi(labelStr)
		scored = append(scored, scoredSource{src: s, height: h})
	}

	// Sort descending by height
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].height > scored[j].height
	})

	tq := strings.ToLower(strings.TrimSpace(targetQuality))
	if tq == "low" || tq == "worst" {
		return scored[len(scored)-1].src
	}
	if tq == "" || tq == "best" {
		return scored[0].src
	}

	targetHeight, err := strconv.Atoi(strings.TrimSuffix(tq, "p"))
	if err != nil {
		return scored[0].src
	}

	// Exact match first
	for _, sc := range scored {
		if sc.height == targetHeight {
			return sc.src
		}
	}

	// Closest <= targetHeight
	for _, sc := range scored {
		if sc.height <= targetHeight {
			return sc.src
		}
	}

	return scored[0].src
}

// IsNoodleNetworkContent detects whether an arbitrary HTML document belongs to a NoodleMagazine/Mat6tube proxy or clone
func IsNoodleNetworkContent(body string) bool {
	return strings.Contains(body, "window.playlist") &&
		(strings.Contains(body, "pvvstream.pro") ||
			strings.Contains(body, "nmcorp.video") ||
			strings.Contains(body, "BEST XXX TUBE") ||
			strings.Contains(body, "NoodleMagazine") ||
			strings.Contains(body, "noodlemagazine") ||
			strings.Contains(body, "mat6tube"))
}

func init() {
	Register(&NoodleMagazineExtractor{})
}
