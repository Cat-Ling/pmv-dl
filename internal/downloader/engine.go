package downloader

import (
	"context"
	"fmt"
	"net/http"

	"pmv-dl/internal/common"
	"pmv-dl/internal/extractor"
	"pmv-dl/internal/postprocessor"
)

func Download(ctx context.Context, client *http.Client, res *extractor.Result, opts common.DownloadOptions) error {
	if res == nil {
		return fmt.Errorf("extraction result is nil")
	}

	switch res.Type {
	case extractor.ResultTypeVideo:
		if res.Video == nil {
			return fmt.Errorf("video result is missing")
		}
		filePath, err := DownloadVideo(ctx, client, res.Video, opts)
		if err != nil {
			return err
		}
		if filePath == "-" || opts.ToStdout {
			return nil
		}
		return postprocessor.RunPipeline(ctx, &postprocessor.MediaInfo{
			FilePath: filePath,
			Title:    res.Video.Title,
			Referer:  res.Video.Referer,
		}, opts)

	case extractor.ResultTypeImage:
		if res.Image == nil {
			return fmt.Errorf("image result is missing")
		}
		return DownloadImage(ctx, client, res.Image, opts.OutputDir)

	case extractor.ResultTypePlaylist:
		if res.Playlist == nil {
			return fmt.Errorf("playlist result is missing")
		}
		return DownloadPlaylist(ctx, client, res.Playlist, opts)

	default:
		return fmt.Errorf("unsupported result type %q", res.Type)
	}
}
