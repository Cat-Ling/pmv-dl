package downloader

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"pmv-dl/internal/common"
	"pmv-dl/internal/extractor"
	"pmv-dl/internal/postprocessor"
)

func DownloadPlaylist(ctx context.Context, client *http.Client, pr *extractor.PlaylistResult, opts common.DownloadOptions) error {
	playlistDir := filepath.Join(opts.OutputDir, common.SanitizeFilename(pr.Title))
	if err := os.MkdirAll(playlistDir, 0755); err != nil {
		return fmt.Errorf("failed to create playlist directory %s: %w", playlistDir, err)
	}

	totalEntries := len(pr.Entries)
	if totalEntries == 0 {
		return fmt.Errorf("playlist %q is empty", pr.Title)
	}

	selectedIndices := common.ParsePlaylistIndices(opts.PlaylistItems, totalEntries, opts.PlaylistStart, opts.PlaylistEnd)

	var filteredEntries []extractor.PlaylistEntry
	for i := 1; i <= totalEntries; i++ {
		if !selectedIndices[i] {
			continue
		}
		entry := pr.Entries[i-1]

		// Filter images or videos
		if opts.NoImages && entry.Type == extractor.ResultTypeImage {
			continue
		}
		if opts.ImagesOnly && entry.Type != extractor.ResultTypeImage {
			continue
		}

		filteredEntries = append(filteredEntries, entry)
	}

	if len(filteredEntries) == 0 {
		fmt.Printf("No items selected in playlist/gallery %q with the current filters.\n", pr.Title)
		return nil
	}

	fmt.Printf("\nDownloading playlist/gallery: %s (%d of %d items selected)\n", pr.Title, len(filteredEntries), totalEntries)
	fmt.Printf("Saving into: %s\n\n", playlistDir)

	for idx, entry := range filteredEntries {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		fmt.Printf("[%d/%d] Processing %s: %s\n", idx+1, len(filteredEntries), entry.Type, entry.Title)

		switch entry.Type {
		case extractor.ResultTypeVideo:
			var vr *extractor.VideoResult
			if entry.StreamURL != "" {
				vr = &extractor.VideoResult{
					Title:     entry.Title,
					PageURL:   entry.URL,
					StreamURL: entry.StreamURL,
					Referer:   entry.URL,
				}
			} else {
				entryURL, err := common.ValidateRemoteURL(entry.URL)
				if err != nil {
					log.Printf("Warning: skipping invalid item URL %s: %v", entry.URL, err)
					continue
				}
				extr := extractor.Find(entryURL)
				if extr == nil {
					log.Printf("Warning: no extractor found for %s", entry.URL)
					continue
				}
				// Force NoPlaylist so it extracts the single video
				singleOpts := opts
				singleOpts.NoPlaylist = true
				res, err := extr.Extract(ctx, client, entryURL, singleOpts)
				if err != nil {
					log.Printf("Warning: failed to extract item %s: %v", entry.URL, err)
					continue
				}
				if res.Type != extractor.ResultTypeVideo || res.Video == nil {
					log.Printf("Warning: expected video result for %s", entry.URL)
					continue
				}
				vr = res.Video
			}

			itemOpts := opts
			itemOpts.OutputDir = playlistDir
			filePath, err := DownloadVideo(ctx, client, vr, itemOpts)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				log.Printf("Warning: failed to download video %s: %v", entry.Title, err)
			} else if filePath != "" && filePath != "-" && !itemOpts.ToStdout {
				if ppErr := postprocessor.RunPipeline(ctx, &postprocessor.MediaInfo{
					FilePath: filePath,
					Title:    vr.Title,
					Referer:  vr.Referer,
				}, itemOpts); ppErr != nil {
					log.Printf("Warning: postprocessing failed for %s: %v", entry.Title, ppErr)
				}
			}

		case extractor.ResultTypeImage:
			var ir *extractor.ImageResult
			if len(entry.ImageURLs) > 0 {
				ir = &extractor.ImageResult{
					Title:     entry.Title,
					PageURL:   entry.URL,
					ImageURLs: entry.ImageURLs,
					Referer:   entry.URL,
				}
			} else {
				entryURL, err := common.ValidateRemoteURL(entry.URL)
				if err != nil {
					log.Printf("Warning: skipping invalid image URL %s: %v", entry.URL, err)
					continue
				}
				extr := extractor.Find(entryURL)
				if extr == nil {
					log.Printf("Warning: no extractor found for %s", entry.URL)
					continue
				}
				res, err := extr.Extract(ctx, client, entryURL, opts)
				if err != nil {
					log.Printf("Warning: failed to extract image item %s: %v", entry.URL, err)
					continue
				}
				if res.Type != extractor.ResultTypeImage || res.Image == nil {
					log.Printf("Warning: expected image result for %s", entry.URL)
					continue
				}
				ir = res.Image
			}

			if err := DownloadImage(ctx, client, ir, playlistDir); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				log.Printf("Warning: failed to download image %s: %v", entry.Title, err)
			}

		default:
			log.Printf("Warning: unsupported playlist entry type %s for %s", entry.Type, entry.Title)
		}

		fmt.Println()
	}

	fmt.Printf("Finished downloading playlist/gallery: %s\n", pr.Title)
	return nil
}
