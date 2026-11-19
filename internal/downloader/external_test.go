package downloader

import (
	"reflect"
	"testing"
)

func TestSelectDownloader(t *testing.T) {
	tests := []struct {
		protocol string
		spec     string
		want     string
	}{
		{"m3u8", "", "native"},
		{"http", "", "native"},
		{"m3u8", "ffmpeg", "ffmpeg"},
		{"http", "aria2c", "aria2c"},
		{"m3u8", "m3u8:native,http:aria2c", "native"},
		{"http", "m3u8:native,http:aria2c", "aria2c"},
		{"https", "m3u8:native,http:aria2c", "aria2c"},
		{"m3u8", "dash,m3u8:ffmpeg,default:aria2c", "ffmpeg"},
		{"http", "dash,m3u8:ffmpeg,default:aria2c", "aria2c"},
		{"ftp", "dash,m3u8:ffmpeg,default:aria2c", "aria2c"},
	}

	for _, tt := range tests {
		got := SelectDownloader(tt.protocol, tt.spec)
		if got != tt.want {
			t.Errorf("SelectDownloader(%q, %q) = %q; want %q", tt.protocol, tt.spec, got, tt.want)
		}
	}
}

func TestParseDownloaderArgs(t *testing.T) {
	tests := []struct {
		spec string
		want map[string][]string
	}{
		{"", map[string][]string{}},
		{"-loglevel warning", map[string][]string{"default": {"-loglevel", "warning"}}},
		{"ffmpeg:-loglevel warning -stats", map[string][]string{"ffmpeg": {"-loglevel", "warning", "-stats"}}},
		{"aria2c:-j8 -s8", map[string][]string{"aria2c": {"-j8", "-s8"}}},
	}

	for _, tt := range tests {
		got := ParseDownloaderArgs(tt.spec)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseDownloaderArgs(%q) = %v; want %v", tt.spec, got, tt.want)
		}
	}
}
