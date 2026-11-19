package downloader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/grafov/m3u8"
	"pmv-dl/internal/common"
	"pmv-dl/internal/extractor"
)

type progressReader struct {
	io.Reader
	counter *uint64
}

func (pr *progressReader) Read(p []byte) (n int, err error) {
	n, err = pr.Reader.Read(p)
	if n > 0 {
		atomic.AddUint64(pr.counter, uint64(n))
	}
	return
}

func DownloadVideo(ctx context.Context, client *http.Client, vr *extractor.VideoResult, opts common.DownloadOptions) (string, error) {
	outDir := opts.OutputDir
	quality := opts.Quality
	threads := opts.Threads
	if vr != nil {
		vr.Title = common.SanitizeFilename(vr.Title)
	}

	if opts.ToStdout || outDir == "-" {
		opts.ToStdout = true
	} else {
		if err := os.MkdirAll(outDir, 0755); err != nil {
			return "", fmt.Errorf("failed to create output directory %s: %w", outDir, err)
		}
	}

	workerCount := threads
	if workerCount < 1 {
		workerCount = 1
	} else if workerCount > common.MaxThreads {
		workerCount = common.MaxThreads
	}

	m3u8URL, err := common.ValidateRemoteURL(vr.StreamURL)
	if err != nil {
		return "", fmt.Errorf("invalid stream URL %q: %w", vr.StreamURL, err)
	}

	// External downloader dispatch
	streamProto := "http"
	if strings.Contains(strings.ToLower(m3u8URL.Path), ".m3u8") {
		streamProto = "m3u8"
	}

	ed := SelectDownloader(streamProto, opts.Downloader)
	if ed != "native" && ed != "" {
		argsMap := ParseDownloaderArgs(opts.DownloaderArgs)
		extraArgs := argsMap[ed]
		if len(extraArgs) == 0 {
			extraArgs = argsMap["default"]
		}

		rawExt := common.DetermineExt(m3u8URL.Path, "mp4")
		if common.IsManifestExtension(rawExt) {
			rawExt = "mp4"
		}
		targetExt := common.SanitizeExtension(rawExt, "mp4")
		outputFile := filepath.Join(outDir, vr.Title+targetExt)

		extVR := *vr
		if client != nil && client.Jar != nil {
			if cookieHdr := common.CookieHeaderForURL(client.Jar, m3u8URL); cookieHdr != "" {
				hdrCopy := make(map[string]string, len(vr.Headers)+1)
				for k, v := range vr.Headers {
					hdrCopy[k] = v
				}
				hdrCopy["Cookie"] = cookieHdr
				extVR.Headers = hdrCopy
			}
		}

		switch ed {
		case "ffmpeg":
			err := RunFFmpegDownloader(ctx, &extVR, vr.StreamURL, outputFile, opts.ToStdout, extraArgs, opts.FFmpegLocation)
			if err != nil {
				return "", err
			}
			if opts.ToStdout {
				return "-", nil
			}
			return outputFile, nil

		case "aria2c":
			if opts.ToStdout {
				return "", fmt.Errorf("aria2c does not support streaming to stdout; use --downloader ffmpeg or native")
			}
			err := RunAria2cDownloader(ctx, &extVR, vr.StreamURL, outputFile, extraArgs)
			if err != nil {
				return "", err
			}
			return outputFile, nil

		case "curl":
			err := RunCurlDownloader(ctx, &extVR, vr.StreamURL, outputFile, opts.ToStdout, extraArgs)
			if err != nil {
				return "", err
			}
			if opts.ToStdout {
				return "-", nil
			}
			return outputFile, nil

		case "wget":
			err := RunWgetDownloader(ctx, &extVR, vr.StreamURL, outputFile, opts.ToStdout, extraArgs)
			if err != nil {
				return "", err
			}
			if opts.ToStdout {
				return "-", nil
			}
			return outputFile, nil

		default:
			return "", fmt.Errorf("unsupported downloader %q; supported: native, ffmpeg, aria2c, curl, wget", ed)
		}
	}

	if isDirectVideoURL(m3u8URL) {
		return downloadDirectVideo(ctx, client, vr, m3u8URL, opts)
	}

	m3u8Bytes, finalM3u8URL, err := common.FetchBytesWithRetry(ctx, client, m3u8URL.String(), vr.Referer, common.MaxPlaylistBytes, common.DefaultTimeout)
	if err != nil {
		return downloadDirectVideo(ctx, client, vr, m3u8URL, opts)
	}
	if finalM3u8URL != nil {
		m3u8URL = finalM3u8URL
	}

	if isDirectVideoURL(m3u8URL) {
		return downloadDirectVideo(ctx, client, vr, m3u8URL, opts)
	}

	playlist, listType, err := DecodeHLSPlaylist(m3u8Bytes)
	if err != nil {
		return downloadDirectVideo(ctx, client, vr, m3u8URL, opts)
	}

	var mediaPl *m3u8.MediaPlaylist
	var chunklistURL *url.URL
	selectedRes := "default"

	switch listType {
	case m3u8.MASTER:
		masterPl := playlist.(*m3u8.MasterPlaylist)
		bestVariant := SelectVariant(masterPl.Variants, quality)
		if bestVariant == nil {
			return "", fmt.Errorf("no valid stream variants found in master playlist")
		}
		if bestVariant.Resolution != "" {
			selectedRes = bestVariant.Resolution
		}

		for _, alt := range bestVariant.Alternatives {
			if alt != nil && strings.EqualFold(alt.Type, "AUDIO") && strings.TrimSpace(alt.URI) != "" {
				log.Printf("Warning: selected variant references a separate audio rendition (%s); output .ts may not include audio", alt.URI)
				break
			}
		}

		chunklistURL, err = common.ResolveURL(m3u8URL, bestVariant.URI)
		if err != nil {
			return "", fmt.Errorf("failed to resolve chunklist URL: %w", err)
		}

		chunkBytes, finalChunkURL, err := common.FetchBytesWithRetry(ctx, client, chunklistURL.String(), vr.Referer, common.MaxPlaylistBytes, common.DefaultTimeout)
		if err != nil {
			return "", fmt.Errorf("failed to fetch chunklist: %w", err)
		}
		if finalChunkURL != nil {
			chunklistURL = finalChunkURL
		}

		mediaPl, err = DecodeMediaPlaylist(chunkBytes)
		if err != nil {
			return "", fmt.Errorf("failed to decode chunklist: %w", err)
		}

	case m3u8.MEDIA:
		mediaPl = playlist.(*m3u8.MediaPlaylist)
		chunklistURL = m3u8URL

	default:
		return "", fmt.Errorf("unsupported playlist type: %v", listType)
	}

	var validSegments []*m3u8.MediaSegment
	for _, s := range mediaPl.Segments {
		if s != nil && strings.TrimSpace(s.URI) != "" {
			validSegments = append(validSegments, s)
		}
	}
	if len(validSegments) == 0 {
		return "", fmt.Errorf("no media segments found in playlist")
	}

	if err := ValidateMediaPlaylistSupport(mediaPl, validSegments); err != nil {
		return "", fmt.Errorf("unsupported HLS stream format: %w", err)
	}

	if opts.ToStdout {
		fmt.Fprintf(os.Stderr, "Streaming %d segments to stdout (Resolution: %s)...\n", len(validSegments), selectedRes)
		for i, seg := range validSegments {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			segURL, err := common.ResolveURL(chunklistURL, seg.URI)
			if err != nil {
				return "", fmt.Errorf("failed to resolve segment %d URL: %w", i, err)
			}
			segBytes, _, err := common.FetchBytesWithRetry(ctx, client, segURL.String(), vr.Referer, common.MaxSegmentBytes, common.DefaultTimeout)
			if err != nil {
				return "", fmt.Errorf("failed to fetch segment %d: %w", i, err)
			}
			if _, err := os.Stdout.Write(segBytes); err != nil {
				return "", fmt.Errorf("failed piping segment %d to stdout: %w", i, err)
			}
			fmt.Fprintf(os.Stderr, "\r\033[KStreaming: [%d/%d] segments piped to stdout", i+1, len(validSegments))
		}
		fmt.Fprintf(os.Stderr, "\nFinished streaming to stdout.\n")
		return "-", nil
	}

	var existingFiles []string
	baseTitle := vr.Title
	title := baseTitle
	outputFile := filepath.Join(outDir, title+".ts")
	for counter := 1; ; counter++ {
		info, err := os.Stat(outputFile)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("failed to inspect output path %s: %w", outputFile, err)
		}
		if !info.IsDir() && info.Size() > 0 {
			existingFiles = append(existingFiles, outputFile)
		}
		title = fmt.Sprintf("%s_%d", baseTitle, counter)
		outputFile = filepath.Join(outDir, title+".ts")
	}

	if len(existingFiles) > 0 {
		match, err := FindExistingMatch(ctx, client, existingFiles, chunklistURL, validSegments, vr.Referer)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err != nil {
			log.Printf("Warning: could not verify existing files (%v); proceeding with new file %s", err, outputFile)
		} else if match != "" {
			fmt.Printf("Already downloaded: %s\n", match)
			return match, nil
		}
	}

	stableStreamKey := strings.ToLower(chunklistURL.Host) + chunklistURL.Path
	streamSum := sha256.Sum256([]byte(stableStreamKey))
	streamID := hex.EncodeToString(streamSum[:4])
	partsDir := filepath.Join(outDir, fmt.Sprintf(".%s_%s_parts", baseTitle, streamID))
	if err := os.MkdirAll(partsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temporary parts directory: %w", err)
	}

	fmt.Printf("Downloading: %s\n", title)
	fmt.Printf("Downloading %d segments (Resolution: %s)...\n", len(validSegments), selectedRes)

	var completedChunks int32
	var totalDownloadedBytes uint64
	var activeWorkers int32

	jobs := make(chan int, len(validSegments))
	for i := range validSegments {
		partFile := filepath.Join(partsDir, fmt.Sprintf("%05d.ts", i))
		if size, err := ValidateTSSegmentFile(partFile); err == nil {
			atomic.AddInt32(&completedChunks, 1)
			atomic.AddUint64(&totalDownloadedBytes, uint64(size))
			continue
		} else if !os.IsNotExist(err) {
			_ = os.Remove(partFile)
		}
		jobs <- i
	}
	close(jobs)

	pendingCount := len(jobs)
	if pendingCount < workerCount {
		workerCount = pendingCount
	}

	uiCtx, uiCancel := context.WithCancel(context.Background())
	var uiWg sync.WaitGroup
	uiWg.Add(1)
	go func(initialBytes uint64) {
		defer uiWg.Done()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()

		lastBytes := initialBytes
		lastTime := time.Now()

		for {
			select {
			case <-uiCtx.Done():
				return
			case t := <-ticker.C:
				currentBytes := atomic.LoadUint64(&totalDownloadedBytes)
				completed := atomic.LoadInt32(&completedChunks)
				workers := atomic.LoadInt32(&activeWorkers)

				duration := t.Sub(lastTime).Seconds()
				var speed float64
				if duration > 0 && currentBytes >= lastBytes {
					speed = float64(currentBytes-lastBytes) / duration
				}

				lastBytes = currentBytes
				lastTime = t

				percent := float64(completed) / float64(len(validSegments)) * 100

				fmt.Printf("\r\033[K[%s] %.1f%% (%d/%d parts) | %s | %s/s | Workers: %d",
					common.FormatProgressBar(percent, 20),
					percent, completed, len(validSegments),
					common.FormatBytes(currentBytes),
					common.FormatBytes(uint64(speed)),
					workers,
				)
			}
		}
	}(atomic.LoadUint64(&totalDownloadedBytes))

	downloadCtx, downloadCancel := context.WithCancel(ctx)
	defer downloadCancel()

	var wg sync.WaitGroup
	errChan := make(chan error, workerCount+1)

	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if downloadCtx.Err() != nil {
					return
				}

				seg := validSegments[i]
				segURL, err := common.ResolveURL(chunklistURL, seg.URI)
				if err != nil {
					select {
					case errChan <- fmt.Errorf("segment %d invalid URL: %w", i, err):
					default:
					}
					downloadCancel()
					return
				}

				partFile := filepath.Join(partsDir, fmt.Sprintf("%05d.ts", i))
				tmpPartFile := partFile + ".tmp"

				var lastErr error
				waitTime := 2 * time.Second

				atomic.AddInt32(&activeWorkers, 1)
				for attempt := 1; attempt <= common.MaxRetries; attempt++ {
					if downloadCtx.Err() != nil {
						lastErr = downloadCtx.Err()
						break
					}

					if attempt > 1 {
						atomic.AddInt32(&activeWorkers, -1)
						select {
						case <-time.After(waitTime):
						case <-downloadCtx.Done():
							atomic.AddInt32(&activeWorkers, 1)
							lastErr = downloadCtx.Err()
							break
						}
						atomic.AddInt32(&activeWorkers, 1)
						waitTime *= 2
					}

					segCtx, segCancel := context.WithTimeout(downloadCtx, 90*time.Second)
					reqSeg, err := http.NewRequestWithContext(segCtx, http.MethodGet, segURL.String(), nil)
					if err != nil {
						segCancel()
						lastErr = err
						continue
					}
					reqSeg.Header.Set("User-Agent", common.UserAgent)
					reqSeg.Header.Set("Referer", vr.Referer)
					reqSeg.Header.Set("Accept-Encoding", "identity")

					respSeg, err := client.Do(reqSeg)
					if err != nil {
						segCancel()
						lastErr = err
						continue
					}

					if respSeg.StatusCode != http.StatusOK {
						wait := common.RetryDelay(respSeg, waitTime)
						retry := common.ShouldRetryStatus(respSeg.StatusCode)
						statusErr := fmt.Errorf("unexpected HTTP status %d", respSeg.StatusCode)
						respSeg.Body.Close()
						segCancel()
						lastErr = statusErr
						if !retry {
							break
						}
						waitTime = wait
						continue
					}

					expectedLen := respSeg.ContentLength

					out, err := os.OpenFile(tmpPartFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
					if err != nil {
						respSeg.Body.Close()
						segCancel()
						lastErr = err
						continue
					}

					pr := &progressReader{
						Reader:  io.LimitReader(respSeg.Body, common.MaxSegmentBytes+1),
						counter: &totalDownloadedBytes,
					}
					written, copyErr := io.Copy(out, pr)
					syncErr := out.Sync()
					closeErr := out.Close()
					respSeg.Body.Close()
					segCancel()

					var segErr error
					switch {
					case copyErr != nil:
						segErr = copyErr
					case syncErr != nil:
						segErr = syncErr
					case closeErr != nil:
						segErr = closeErr
					case written > common.MaxSegmentBytes:
						segErr = fmt.Errorf("segment exceeded maximum size of %d bytes", common.MaxSegmentBytes)
					case expectedLen > 0 && written != expectedLen:
						segErr = fmt.Errorf("incomplete segment read (%d of %d bytes)", written, expectedLen)
					default:
						_, segErr = ValidateTSSegmentFile(tmpPartFile)
					}

					if segErr != nil {
						if written > 0 {
							atomic.AddUint64(&totalDownloadedBytes, ^uint64(written-1))
						}
						_ = os.Remove(tmpPartFile)
						lastErr = segErr
						continue
					}

					if err := os.Rename(tmpPartFile, partFile); err != nil {
						if written > 0 {
							atomic.AddUint64(&totalDownloadedBytes, ^uint64(written-1))
						}
						_ = os.Remove(tmpPartFile)
						lastErr = err
						continue
					}

					lastErr = nil
					break
				}
				atomic.AddInt32(&activeWorkers, -1)

				if lastErr != nil {
					if ctx.Err() == nil {
						select {
						case errChan <- fmt.Errorf("segment %d failed: %w", i, lastErr):
						default:
						}
					}
					downloadCancel()
					return
				}

				atomic.AddInt32(&completedChunks, 1)
			}
		}()
	}

	wg.Wait()
	uiCancel()
	uiWg.Wait()
	close(errChan)

	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	if err, ok := <-errChan; ok && err != nil {
		return "", err
	}

	currentBytes := atomic.LoadUint64(&totalDownloadedBytes)
	fmt.Printf("\r\033[K[%s] 100.0%% (%d/%d parts) | %s | 0 B/s | Workers: 0\n",
		common.FormatProgressBar(100.0, 20),
		len(validSegments), len(validSegments),
		common.FormatBytes(currentBytes),
	)

	fmt.Printf("Stitching segments into %s...\n", outputFile)

	tmpOutputFile := outputFile + ".tmp"
	finalOut, err := os.OpenFile(tmpOutputFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return "", fmt.Errorf("failed to create temporary output file: %w", err)
	}

	for i := range validSegments {
		if ctx.Err() != nil {
			_ = finalOut.Close()
			_ = os.Remove(tmpOutputFile)
			return "", ctx.Err()
		}

		partPath := filepath.Join(partsDir, fmt.Sprintf("%05d.ts", i))
		expectedSize, err := ValidateTSSegmentFile(partPath)
		if err != nil {
			_ = finalOut.Close()
			_ = os.Remove(tmpOutputFile)
			return "", fmt.Errorf("invalid segment part %s: %w", partPath, err)
		}

		in, err := os.Open(partPath)
		if err != nil {
			_ = finalOut.Close()
			_ = os.Remove(tmpOutputFile)
			return "", fmt.Errorf("failed to open part %s: %w", partPath, err)
		}

		copied, copyErr := io.Copy(finalOut, in)
		closeErr := in.Close()
		if copyErr != nil || closeErr != nil || copied != expectedSize {
			_ = finalOut.Close()
			_ = os.Remove(tmpOutputFile)
			if copyErr != nil {
				return "", fmt.Errorf("failed to write part %s: %w", partPath, copyErr)
			}
			if closeErr != nil {
				return "", fmt.Errorf("failed to close part %s: %w", partPath, closeErr)
			}
			return "", fmt.Errorf("short write for part %s (%d of %d bytes)", partPath, copied, expectedSize)
		}
	}

	if err := finalOut.Sync(); err != nil {
		_ = finalOut.Close()
		_ = os.Remove(tmpOutputFile)
		return "", fmt.Errorf("failed to flush output file to disk: %w", err)
	}

	if err := finalOut.Close(); err != nil {
		_ = os.Remove(tmpOutputFile)
		return "", fmt.Errorf("failed to close output file: %w", err)
	}

	if err := os.Rename(tmpOutputFile, outputFile); err != nil {
		_ = os.Remove(tmpOutputFile)
		return "", fmt.Errorf("failed to move completed file into place: %w", err)
	}

	if err := os.RemoveAll(partsDir); err != nil {
		log.Printf("Warning: failed to remove temporary parts directory %s: %v", partsDir, err)
	}

	fmt.Printf("Success: %s saved to %s\n", title, outputFile)
	return outputFile, nil
}

func isDirectVideoURL(u *url.URL) bool {
	return common.IsDirectMediaURL(u)
}

func downloadDirectVideo(ctx context.Context, client *http.Client, vr *extractor.VideoResult, targetURL *url.URL, opts common.DownloadOptions) (string, error) {
	if opts.ToStdout {
		req, err := http.NewRequestWithContext(ctx, "GET", targetURL.String(), nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", common.UserAgent)
		if vr != nil && vr.Referer != "" {
			req.Header.Set("Referer", vr.Referer)
		}
		if vr != nil {
			for k, v := range vr.Headers {
				req.Header.Set(k, v)
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("failed to stream direct video: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			return "", fmt.Errorf("unexpected status %d while streaming to stdout", resp.StatusCode)
		}

		_, err = io.Copy(os.Stdout, resp.Body)
		if err != nil {
			return "", fmt.Errorf("failed piping direct video to stdout: %w", err)
		}
		return "-", nil
	}

	outDir := opts.OutputDir
	rawExt := common.DetermineExt(targetURL.Path, "")
	urlHasDirectExt := common.IsDirectMediaExtension(rawExt)
	if !urlHasDirectExt {
		rawExt = "mp4"
	}
	ext := common.SanitizeExtension(rawExt, "mp4")
	title := common.SanitizeFilename(vr.Title)
	outputFile := filepath.Join(outDir, title+ext)
	partFile := outputFile + ".part"

	var totalSize int64 = -1
	if stat, err := os.Stat(outputFile); err == nil && stat.Size() > 0 {
		if req, reqErr := http.NewRequestWithContext(ctx, "HEAD", targetURL.String(), nil); reqErr == nil {
			req.Header.Set("User-Agent", common.UserAgent)
			if vr.Referer != "" {
				req.Header.Set("Referer", vr.Referer)
			}
			for k, v := range vr.Headers {
				req.Header.Set(k, v)
			}
			if resp, doErr := client.Do(req); doErr == nil {
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					totalSize = resp.ContentLength
				}
				_ = resp.Body.Close()
			}
		}
		if totalSize > 0 && stat.Size() == totalSize {
			fmt.Printf("File already downloaded: %s\n", outputFile)
			return outputFile, nil
		}
		if totalSize <= 0 {
			fmt.Printf("File already exists: %s\n", outputFile)
			return outputFile, nil
		}
	}

	fmt.Printf("Downloading: %s\n", title)

	var startOffset int64 = 0
	if stat, err := os.Stat(partFile); err == nil && stat.Size() > 0 {
		startOffset = stat.Size()
	}

	getReq, err := http.NewRequestWithContext(ctx, "GET", targetURL.String(), nil)
	if err != nil {
		return "", err
	}
	getReq.Header.Set("User-Agent", common.UserAgent)
	if vr.Referer != "" {
		getReq.Header.Set("Referer", vr.Referer)
	}
	for k, v := range vr.Headers {
		getReq.Header.Set(k, v)
	}

	if startOffset > 0 {
		getReq.Header.Set("Range", fmt.Sprintf("bytes=%d-", startOffset))
	}

	getResp, err := client.Do(getReq)
	if err != nil {
		return "", fmt.Errorf("failed to start download: %w", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		if totalSize > 0 && startOffset >= totalSize {
			if err := os.Rename(partFile, outputFile); err == nil {
				fmt.Printf("File already downloaded: %s\n", outputFile)
				return outputFile, nil
			}
		}
		// Reset offset and retry without Range
		startOffset = 0
		getReq.Header.Del("Range")
		retryResp, err := client.Do(getReq)
		if err != nil {
			return "", fmt.Errorf("failed to restart download from beginning: %w", err)
		}
		getResp.Body.Close()
		getResp = retryResp
	}

	if getResp.StatusCode != http.StatusOK && getResp.StatusCode != http.StatusPartialContent {
		return "", fmt.Errorf("unexpected status %d while downloading %s", getResp.StatusCode, targetURL.String())
	}

	if !urlHasDirectExt && startOffset == 0 {
		if hdrExt := common.DetectExtFromHeaders(getResp.Header, ""); common.IsDirectMediaExtension(hdrExt) {
			ext = common.SanitizeExtension(hdrExt, "mp4")
			outputFile = filepath.Join(outDir, title+ext)
			partFile = outputFile + ".part"
		}
	}

	var out *os.File
	if getResp.StatusCode == http.StatusPartialContent {
		out, err = os.OpenFile(partFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	} else {
		startOffset = 0
		out, err = os.OpenFile(partFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	}
	if err != nil {
		return "", fmt.Errorf("failed to open part file: %w", err)
	}

	if totalSize <= 0 && getResp.ContentLength > 0 {
		totalSize = startOffset + getResp.ContentLength
	}

	var downloaded uint64 = uint64(startOffset)
	pr := &progressReader{
		Reader:  getResp.Body,
		counter: &downloaded,
	}

	uiCtx, uiCancel := context.WithCancel(ctx)
	defer uiCancel()
	var uiWg sync.WaitGroup
	uiWg.Add(1)
	go func() {
		defer uiWg.Done()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		lastBytes := downloaded
		lastTime := time.Now()

		for {
			select {
			case <-uiCtx.Done():
				return
			case now := <-ticker.C:
				currBytes := atomic.LoadUint64(&downloaded)
				deltaBytes := currBytes - lastBytes
				deltaTime := now.Sub(lastTime).Seconds()
				var speed float64
				if deltaTime > 0 {
					speed = float64(deltaBytes) / deltaTime
				}
				lastBytes = currBytes
				lastTime = now

				if totalSize > 0 {
					percent := (float64(currBytes) / float64(totalSize)) * 100.0
					fmt.Printf("\r\033[K[%s] %.1f%% | %s / %s | %s/s",
						common.FormatProgressBar(percent, 20),
						percent,
						common.FormatBytes(currBytes),
						common.FormatBytes(uint64(totalSize)),
						common.FormatBytes(uint64(speed)),
					)
				} else {
					fmt.Printf("\r\033[K%s | %s/s",
						common.FormatBytes(currBytes),
						common.FormatBytes(uint64(speed)),
					)
				}
			}
		}
	}()

	_, copyErr := io.Copy(out, pr)
	syncErr := out.Sync()
	closeErr := out.Close()
	uiCancel()
	uiWg.Wait()

	if copyErr != nil {
		return "", fmt.Errorf("download interrupted: %w", copyErr)
	}
	if syncErr != nil {
		return "", fmt.Errorf("failed to sync part file: %w", syncErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("failed to close part file: %w", closeErr)
	}

	if err := os.Rename(partFile, outputFile); err != nil {
		return "", fmt.Errorf("failed to finalize file: %w", err)
	}

	finalBytes := atomic.LoadUint64(&downloaded)
	fmt.Printf("\r\033[K[%s] 100.0%% | %s | 0 B/s\n",
		common.FormatProgressBar(100.0, 20),
		common.FormatBytes(finalBytes),
	)
	fmt.Printf("Success: %s saved to %s\n", title, outputFile)
	return outputFile, nil
}
