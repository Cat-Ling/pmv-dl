package extractor

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"pmv-dl/internal/common"
)

var (
	reSpreeeVideoPath = regexp.MustCompile(`^/([0-9]+)/?$`)
	reSpreeeVideoLink = regexp.MustCompile(`href=["']/([0-9]+)/?["']`)
	reSpreeeSource    = regexp.MustCompile(`(?is)<source[^>]+src=["'](https?://[^"']+\.mp4[^"']*)["']`)
	reSpreeeYAVideo   = regexp.MustCompile(`(?is)<meta[^>]+property=["']ya:ovs:content_url["'][^>]+content=["'](https?://[^"']+)["']`)
	reSpreeeOGTitle   = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:title["'][^>]+content=["'](.*?)["']\s*(?:/?>|[a-zA-Z_-]+=)`)
	reSpreeeTitleTag  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reSpreeeSuffix    = regexp.MustCompile(`(?i)\s*[-—|]\s*(?:spree|itsmyfetish).*$`)
	reSpreeeHTMLTags  = regexp.MustCompile(`<[^>]*>`)
)

type SpreeeExtractor struct{}

func (s *SpreeeExtractor) Name() string {
	return "spreee"
}

func (s *SpreeeExtractor) Description() string {
	return "Spreee, bbb.spreee.pro, and spree.tube video and collection extractor"
}

func (s *SpreeeExtractor) Capabilities() Capability {
	return CapVideo | CapPlaylist
}

func (s *SpreeeExtractor) Match(u *url.URL) bool {
	host := strings.ToLower(u.Host)
	if colon := strings.Index(host, ":"); colon != -1 {
		host = host[:colon]
	}

	matchesDomain := func(d string) bool {
		return host == d || strings.HasSuffix(host, "."+d)
	}

	return matchesDomain("spreee.pro") ||
		matchesDomain("spree.tube") ||
		matchesDomain("itsmyfetish.com") ||
		matchesDomain("embed-player.space")
}

func (s *SpreeeExtractor) Extract(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	// Normalize domain: bare spreee.pro usually fails or redirects, while bbb.spreee.pro serves content
	normalizedURL := *targetURL
	host := strings.ToLower(normalizedURL.Host)
	if host == "spreee.pro" || host == "www.spreee.pro" {
		normalizedURL.Host = "bbb.spreee.pro"
	} else if host == "spree.tube" || host == "www.spree.tube" {
		normalizedURL.Host = "xxx.spree.tube"
	}

	urlStr := normalizedURL.String()
	referer := normalizedURL.Scheme + "://" + normalizedURL.Host + "/"
	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, urlStr, referer, common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s: %w", urlStr, err)
	}
	if finalURL != nil {
		normalizedURL = *finalURL
	}

	return s.ExtractFromHTML(ctx, client, &normalizedURL, bodyBytes, opts)
}

func (s *SpreeeExtractor) ExtractFromHTML(ctx context.Context, client *http.Client, targetURL *url.URL, bodyBytes []byte, opts common.DownloadOptions) (*Result, error) {
	bodyStr := string(bodyBytes)
	path := strings.ToLower(targetURL.Path)

	isVideoPage := reSpreeeVideoPath.MatchString(path) || strings.Contains(targetURL.Host, "embed-player.space")
	if !isVideoPage {
		// Category or listing page
		matches := reSpreeeVideoLink.FindAllStringSubmatch(bodyStr, -1)
		if len(matches) > 0 {
			if opts.NoPlaylist {
				firstID := matches[0][1]
				firstURL, _ := url.Parse(fmt.Sprintf("%s://%s/%s/", targetURL.Scheme, targetURL.Host, firstID))
				return s.Extract(ctx, client, firstURL, opts)
			}

			var entries []PlaylistEntry
			seen := make(map[string]bool)
			for _, m := range matches {
				id := m[1]
				vURL := fmt.Sprintf("%s://%s/%s/", targetURL.Scheme, targetURL.Host, id)
				if !seen[vURL] {
					seen[vURL] = true
					entries = append(entries, PlaylistEntry{
						Type:  ResultTypeVideo,
						Title: "spreee_" + id,
						URL:   vURL,
					})
				}
			}

			plTitle := s.extractTitle(bodyStr, targetURL)
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
	title := s.extractTitle(bodyStr, targetURL)
	streamURL := ""

	if m := reSpreeeSource.FindStringSubmatch(bodyStr); len(m) > 1 {
		streamURL = m[1]
	} else if m := reSpreeeYAVideo.FindStringSubmatch(bodyStr); len(m) > 1 {
		streamURL = m[1]
	} else if m := reSpreeeVideoPath.FindStringSubmatch(path); len(m) > 1 {
		streamURL = fmt.Sprintf("https://cf-cdn.embed-player.space/%s.mp4?d=spree", m[1])
	}

	if streamURL == "" {
		return nil, fmt.Errorf("could not find video stream URL in %s", targetURL.String())
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

func (s *SpreeeExtractor) extractTitle(bodyStr string, targetURL *url.URL) string {
	raw := ""
	if m := reSpreeeOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	} else if m := reSpreeeTitleTag.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	}

	raw = reSpreeeHTMLTags.ReplaceAllString(raw, "")
	raw = html.UnescapeString(raw)
	raw = strings.TrimSpace(raw)
	raw = reSpreeeSuffix.ReplaceAllString(raw, "")
	raw = strings.TrimSpace(raw)

	if raw == "" {
		if m := reSpreeeVideoPath.FindStringSubmatch(targetURL.Path); len(m) > 1 {
			raw = "spreee_" + m[1]
		} else {
			raw = "spreee_video"
		}
	}

	return common.SanitizeFilename(raw)
}

func init() {
	Register(&SpreeeExtractor{})
}
