package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"pmv-dl/internal/common"
	"pmv-dl/internal/extractor"
)

func DownloadImageFile(ctx context.Context, client *http.Client, imgURL string, referer string, destPath string) error {
	imgURL = strings.ReplaceAll(imgURL, "https://media.goonchan.org/", "https://test-media.goonchan.org/")

	info, err := os.Stat(destPath)
	if err == nil && !info.IsDir() && info.Size() > 0 {
		fmt.Printf("Already downloaded image: %s\n", destPath)
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", common.UserAgent)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s for image %s", resp.Status, imgURL)
	}

	tmpPath := destPath + ".tmp"
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	written, copyErr := io.Copy(out, resp.Body)
	syncErr := out.Sync()
	closeErr := out.Close()

	if copyErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(tmpPath)
		if copyErr != nil {
			return copyErr
		}
		if syncErr != nil {
			return syncErr
		}
		return closeErr
	}

	if resp.ContentLength > 0 && written != resp.ContentLength {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("short image read: %d of %d bytes", written, resp.ContentLength)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	fmt.Printf("Saved image: %s (%s)\n", filepath.Base(destPath), common.FormatBytes(uint64(written)))
	return nil
}

func DownloadImage(ctx context.Context, client *http.Client, ir *extractor.ImageResult, outDir string) error {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outDir, err)
	}

	if len(ir.ImageURLs) == 0 {
		return fmt.Errorf("no image URLs provided for %s", ir.Title)
	}
	ir.Title = common.SanitizeFilename(ir.Title)

	fmt.Printf("Downloading %d images for: %s\n", len(ir.ImageURLs), ir.Title)
	for i, u := range ir.ImageURLs {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		rawExt := common.DetermineExt(u, "webp")
		if !common.IsImageExtension(rawExt) {
			rawExt = "webp"
		}
		ext := common.SanitizeExtension(rawExt, "webp")

		var fileName string
		if len(ir.ImageURLs) == 1 {
			fileName = fmt.Sprintf("%s%s", ir.Title, ext)
		} else {
			fileName = fmt.Sprintf("%s_%d%s", ir.Title, i, ext)
		}
		dest := filepath.Join(outDir, fileName)

		if err := DownloadImageFile(ctx, client, u, ir.Referer, dest); err != nil {
			return err
		}
	}
	return nil
}
