package postprocessor

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"pmv-dl/internal/common"
)

func TestParsePPA(t *testing.T) {
	tests := []struct {
		ppName string
		spec   string
		want   []string
	}{
		{"ExtractAudio", "", nil},
		{"ExtractAudio", "-b:a 192k", []string{"-b:a", "192k"}},
		{"ExtractAudio", "ExtractAudio:-b:a 256k", []string{"-b:a", "256k"}},
		{"ExtractAudio", "VideoRemuxer:-c copy", nil},
		{"ExtractAudio", "FFmpeg:-loglevel quiet", []string{"-loglevel", "quiet"}},
		{"VideoRemuxer", "default:-stats", []string{"-stats"}},
	}

	for _, tt := range tests {
		got := parsePPA(tt.ppName, tt.spec)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parsePPA(%q, %q) = %v; want %v", tt.ppName, tt.spec, got, tt.want)
		}
	}
}

func TestExecPP(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "sample.txt")
	if err := os.WriteFile(testFile, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	pp := &ExecPP{}
	info := &MediaInfo{
		FilePath: testFile,
		Title:    "Sample",
	}

	outMarker := filepath.Join(tmpDir, "executed.txt")
	opts := common.DownloadOptions{
		ExecCmd: "touch " + outMarker,
	}

	if err := pp.Run(context.Background(), info, opts); err != nil {
		t.Fatalf("ExecPP.Run failed: %v", err)
	}

	if _, err := os.Stat(outMarker); os.IsNotExist(err) {
		t.Fatalf("Expected %s to exist after ExecPP", outMarker)
	}
}

func TestQuoteShell(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple.mp4", "'simple.mp4'"},
		{"file with spaces.mp4", "'file with spaces.mp4'"},
		{"file'with'quote.mp4", "'file'\\''with'\\''quote.mp4'"},
	}

	for _, tt := range tests {
		got := quoteShell(tt.input)
		if got != tt.want {
			t.Errorf("quoteShell(%q) = %q; want %q", tt.input, got, tt.want)
		}
	}
}
