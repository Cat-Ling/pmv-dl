package downloader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/grafov/m3u8"
	"pmv-dl/internal/common"
)

var (
	reURIHeight = regexp.MustCompile(`(?i)(?:^|[_/])(\d{3,4})p\.m3u8`)
)

func DecodeMediaPlaylist(data []byte) (*m3u8.MediaPlaylist, error) {
	segCount := uint(bytes.Count(data, []byte("#EXTINF"))) + 16
	if segCount < 1024 {
		segCount = 1024
	}
	pl, err := m3u8.NewMediaPlaylist(segCount, segCount)
	if err != nil {
		return nil, err
	}
	if err := pl.DecodeFrom(bytes.NewReader(data), true); err != nil {
		return nil, err
	}
	return pl, nil
}

func DecodeHLSPlaylist(data []byte) (m3u8.Playlist, m3u8.ListType, error) {
	if bytes.Contains(data, []byte("#EXTINF")) {
		pl, err := DecodeMediaPlaylist(data)
		if err != nil {
			return nil, 0, err
		}
		return pl, m3u8.MEDIA, nil
	}
	return m3u8.DecodeFrom(bytes.NewReader(data), true)
}

func VariantHeight(v *m3u8.Variant) int {
	if v == nil {
		return 0
	}

	var shortSide, longSide int
	parts := strings.Split(v.Resolution, "x")
	if len(parts) == 2 {
		w, errW := strconv.Atoi(parts[0])
		h, errH := strconv.Atoi(parts[1])
		if errW == nil && errH == nil && w > 0 && h > 0 {
			if w < h {
				shortSide, longSide = w, h
			} else {
				shortSide, longSide = h, w
			}
		}
	}

	var uriH int
	if m := reURIHeight.FindStringSubmatch(v.URI); len(m) > 1 {
		if h, err := strconv.Atoi(m[1]); err == nil && h > 0 {
			uriH = h
		}
	}

	if shortSide > 0 && uriH > 0 {
		if uriH == longSide && shortSide < longSide {
			return shortSide
		}
		return uriH
	}
	if shortSide > 0 {
		return shortSide
	}
	return uriH
}

func SelectVariant(variants []*m3u8.Variant, qualityFlag string) *m3u8.Variant {
	var valid []*m3u8.Variant
	for _, v := range variants {
		if v != nil && strings.TrimSpace(v.URI) != "" {
			valid = append(valid, v)
		}
	}
	if len(valid) == 0 {
		return nil
	}

	qualityStr := strings.ToLower(strings.TrimSpace(qualityFlag))
	isBest := qualityStr == "best"
	isWorst := qualityStr == "worst" || qualityStr == "low"

	if isBest {
		best := valid[0]
		for _, v := range valid[1:] {
			vH, bH := VariantHeight(v), VariantHeight(best)
			if vH > bH || (vH == bH && v.Bandwidth > best.Bandwidth) {
				best = v
			}
		}
		return best
	}

	if isWorst {
		worst := valid[0]
		for _, v := range valid[1:] {
			vH, wH := VariantHeight(v), VariantHeight(worst)
			if vH > 0 && wH > 0 {
				if vH < wH || (vH == wH && v.Bandwidth < worst.Bandwidth) {
					worst = v
				}
			} else if v.Bandwidth < worst.Bandwidth {
				worst = v
			}
		}
		return worst
	}

	targetHeight := 1080
	qStr := strings.TrimSuffix(qualityStr, "p")
	if h, err := strconv.Atoi(qStr); err == nil && h > 0 {
		targetHeight = h
	}

	var bestUnder *m3u8.Variant
	var lowestAbove *m3u8.Variant

	for _, v := range valid {
		h := VariantHeight(v)
		if h <= targetHeight {
			if bestUnder == nil {
				bestUnder = v
			} else {
				bH := VariantHeight(bestUnder)
				if h > bH || (h == bH && v.Bandwidth > bestUnder.Bandwidth) {
					bestUnder = v
				}
			}
		} else {
			if lowestAbove == nil {
				lowestAbove = v
			} else {
				lH := VariantHeight(lowestAbove)
				if h < lH || (h == lH && v.Bandwidth < lowestAbove.Bandwidth) {
					lowestAbove = v
				}
			}
		}
	}

	if bestUnder != nil {
		return bestUnder
	}
	if lowestAbove != nil {
		return lowestAbove
	}
	return valid[0]
}

func ValidateMediaPlaylistSupport(mediaPl *m3u8.MediaPlaylist, segments []*m3u8.MediaSegment) error {
	if !mediaPl.Closed {
		return errors.New("live or incomplete HLS playlists (missing #EXT-X-ENDLIST) are not supported")
	}
	if mediaPl.Key != nil && mediaPl.Key.Method != "" && !strings.EqualFold(mediaPl.Key.Method, "NONE") {
		return fmt.Errorf("encrypted HLS streams (%s) are not supported", mediaPl.Key.Method)
	}
	if mediaPl.Map != nil && mediaPl.Map.URI != "" {
		return errors.New("fMP4 initialization segments (#EXT-X-MAP) are not supported")
	}
	for _, seg := range segments {
		if seg.Key != nil && seg.Key.Method != "" && !strings.EqualFold(seg.Key.Method, "NONE") {
			return fmt.Errorf("encrypted HLS segments (%s) are not supported", seg.Key.Method)
		}
		if seg.Map != nil && seg.Map.URI != "" {
			return errors.New("fMP4 initialization segments (#EXT-X-MAP) are not supported")
		}
		if seg.Limit > 0 {
			return errors.New("byte-range HLS segments (#EXT-X-BYTERANGE) are not supported")
		}
	}
	return nil
}

func ValidateTSSegmentFile(path string) (int64, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if stat.IsDir() {
		return 0, errors.New("segment path is a directory")
	}
	size := stat.Size()
	if size < common.MPEGTSPacketSize || size%common.MPEGTSPacketSize != 0 {
		return 0, fmt.Errorf("invalid MPEG-TS segment size %d", size)
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var header [1]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return 0, err
	}
	if header[0] != common.MPEGTSSyncByte {
		return 0, fmt.Errorf("missing leading MPEG-TS sync byte (got 0x%02x)", header[0])
	}
	if size > common.MPEGTSPacketSize {
		if _, err := f.ReadAt(header[:], size-common.MPEGTSPacketSize); err != nil {
			return 0, err
		}
		if header[0] != common.MPEGTSSyncByte {
			return 0, fmt.Errorf("missing trailing MPEG-TS sync byte (got 0x%02x)", header[0])
		}
	}
	return size, nil
}

func FetchRangeSample(ctx context.Context, client *http.Client, segURL *url.URL, referer, rangeHeader string, maxBytes int) ([]byte, int64, error) {
	waitTime := time.Second
	var lastErr error

	for attempt := 1; attempt <= common.MaxRetries; attempt++ {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		if attempt > 1 {
			select {
			case <-time.After(waitTime):
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			}
			waitTime *= 2
		}

		reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, segURL.String(), nil)
		if err != nil {
			cancel()
			return nil, 0, err
		}
		req.Header.Set("User-Agent", common.UserAgent)
		req.Header.Set("Referer", referer)
		req.Header.Set("Range", rangeHeader)

		resp, err := client.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}

		if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
			retry := common.ShouldRetryStatus(resp.StatusCode)
			statusErr := fmt.Errorf("unexpected status %d", resp.StatusCode)
			resp.Body.Close()
			cancel()
			if !retry {
				return nil, 0, statusErr
			}
			lastErr = statusErr
			continue
		}

		if strings.HasPrefix(rangeHeader, "bytes=-") && resp.StatusCode != http.StatusPartialContent {
			resp.Body.Close()
			cancel()
			return nil, 0, errors.New("server did not honor suffix range request")
		}

		var totalSize int64
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			if idx := strings.LastIndex(cr, "/"); idx != -1 {
				if sz, err := strconv.ParseInt(cr[idx+1:], 10, 64); err == nil && sz > 0 {
					totalSize = sz
				}
			}
		} else if resp.ContentLength > 0 {
			totalSize = resp.ContentLength
		}

		data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)))
		resp.Body.Close()
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		if len(data) == 0 {
			lastErr = errors.New("empty range sample")
			continue
		}
		return data, totalSize, nil
	}

	return nil, 0, lastErr
}

func FileMatchesSamples(path string, headSample, tailSample []byte, minExpectedSize int64) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	fileSize := info.Size()
	if fileSize < int64(len(headSample))+int64(len(tailSample)) || fileSize < minExpectedSize {
		return false
	}

	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	if len(headSample) > 0 {
		buf := make([]byte, len(headSample))
		if _, err := io.ReadFull(f, buf); err != nil || !bytes.Equal(buf, headSample) {
			return false
		}
	}

	if len(tailSample) > 0 {
		buf := make([]byte, len(tailSample))
		if _, err := f.ReadAt(buf, fileSize-int64(len(tailSample))); err != nil || !bytes.Equal(buf, tailSample) {
			return false
		}
	}

	return true
}

func FindExistingMatch(ctx context.Context, client *http.Client, existingFiles []string, chunklistURL *url.URL, validSegments []*m3u8.MediaSegment, referer string) (string, error) {
	if len(existingFiles) == 0 || len(validSegments) == 0 {
		return "", nil
	}

	firstSegURL, err := common.ResolveURL(chunklistURL, validSegments[0].URI)
	if err != nil {
		return "", err
	}

	headSample, firstSegSize, err := FetchRangeSample(ctx, client, firstSegURL, referer, fmt.Sprintf("bytes=0-%d", common.ProbeSampleSize-1), common.ProbeSampleSize)
	if err != nil {
		return "", err
	}

	minExpectedSize := firstSegSize
	var tailSample []byte

	if len(validSegments) > 1 {
		lastSegURL, err := common.ResolveURL(chunklistURL, validSegments[len(validSegments)-1].URI)
		if err != nil {
			return "", err
		}
		sample, lastSegSize, err := FetchRangeSample(ctx, client, lastSegURL, referer, fmt.Sprintf("bytes=-%d", common.ProbeSampleSize), common.ProbeSampleSize)
		if err != nil {
			return "", err
		}
		tailSample = sample
		minExpectedSize += lastSegSize
	} else if firstSegSize > common.ProbeSampleSize {
		sample, _, err := FetchRangeSample(ctx, client, firstSegURL, referer, fmt.Sprintf("bytes=-%d", common.ProbeSampleSize), common.ProbeSampleSize)
		if err != nil {
			return "", err
		}
		tailSample = sample
	}

	for _, candidate := range existingFiles {
		if FileMatchesSamples(candidate, headSample, tailSample, minExpectedSize) {
			return candidate, nil
		}
	}
	return "", nil
}
