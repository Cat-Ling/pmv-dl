package common

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseBrowserSpec(t *testing.T) {
	tests := []struct {
		input     string
		want      BrowserSpec
		expectErr bool
	}{
		{
			input: "chrome",
			want:  BrowserSpec{Browser: "chrome"},
		},
		{
			input: "Chrome:Default",
			want:  BrowserSpec{Browser: "chrome", Profile: "Default"},
		},
		{
			input: "brave+gnomekeyring:Profile 1",
			want:  BrowserSpec{Browser: "brave", Keyring: "gnomekeyring", Profile: "Profile 1"},
		},
		{
			input: "firefox:default-release::personal",
			want:  BrowserSpec{Browser: "firefox", Profile: "default-release", Container: "personal"},
		},
		{
			input:     "",
			expectErr: true,
		},
	}

	for _, tc := range tests {
		got, err := ParseBrowserSpec(tc.input)
		if tc.expectErr {
			if err == nil {
				t.Errorf("ParseBrowserSpec(%q) expected error, got nil", tc.input)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseBrowserSpec(%q) unexpected error: %v", tc.input, err)
		}
		if got != tc.want {
			t.Errorf("ParseBrowserSpec(%q) = %+v, want %+v", tc.input, got, tc.want)
		}
	}
}

func TestDomainMatchingAndRegistrableDomain(t *testing.T) {
	if !DomainMatches(".heavyfetish.com", "www.heavyfetish.com") {
		t.Error("expected .heavyfetish.com to match www.heavyfetish.com")
	}
	if !DomainMatches("heavyfetish.com", "heavyfetish.com") {
		t.Error("expected heavyfetish.com to match heavyfetish.com")
	}
	if DomainMatches("heavyfetish.com", "notheavyfetish.com") {
		t.Error("expected heavyfetish.com not to match notheavyfetish.com")
	}
	if DomainMatches("1.168.192", "192.168.1.1") {
		t.Error("expected IP address not to subdomain-match")
	}
	if got := RegistrableDomain("sub.bbc.co.uk"); got != "bbc.co.uk" {
		t.Errorf("RegistrableDomain(sub.bbc.co.uk) = %q, want bbc.co.uk", got)
	}
	if !SameRegistrableDomain("www.eporner.com", "static.eporner.com") {
		t.Error("expected www.eporner.com and static.eporner.com to share registrable domain")
	}
	if SameRegistrableDomain("www.eporner.com", "cdn.evil.com") {
		t.Error("expected www.eporner.com and cdn.evil.com not to share registrable domain")
	}
}

func TestNetscapeCookieParsingAndIsolation(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}

	targetURL, _ := url.Parse("https://www.heavyfetish.com/videos/12345/sample/")
	evilURL, _ := url.Parse("https://evil.com/track")
	insecureTargetURL, _ := url.Parse("http://www.heavyfetish.com/videos/12345/sample/")

	// 13460277425856900 is Windows FILETIME microseconds (~July 2027)
	// 1000000000 is an expired Unix timestamp (Sept 2001)
	netscapeData := `# Netscape HTTP Cookie File
#HttpOnly_.heavyfetish.com	TRUE	/	TRUE	13460277425856900	bunny_shield	pow_token_abc
heavyfetish.com	FALSE	/	FALSE	0	session_id	sess_999
.heavyfetish.com	TRUE	/	FALSE	1000000000	expired_cookie	old_val
#HttpOnly_.evil.com	TRUE	/	TRUE	13460277425856900	evil_token	steal_me
.google.com	TRUE	/	TRUE	13460277425856900	SID	google_secret
`

	if err := LoadCookiesIntoJar(jar, netscapeData, targetURL); err != nil {
		t.Fatalf("LoadCookiesIntoJar failed: %v", err)
	}

	targetCookies := jar.Cookies(targetURL)
	cookieMap := make(map[string]string)
	for _, c := range targetCookies {
		cookieMap[c.Name] = c.Value
	}

	if cookieMap["bunny_shield"] != "pow_token_abc" {
		t.Errorf("expected bunny_shield=pow_token_abc on www subdomain, got %q", cookieMap["bunny_shield"])
	}
	// Host-only cookie (include_subdomains=FALSE on heavyfetish.com) must only be sent to apex heavyfetish.com, not www.heavyfetish.com
	if _, exists := cookieMap["session_id"]; exists {
		t.Error("expected host-only cookie session_id not to be sent to www subdomain")
	}
	apexURL, _ := url.Parse("https://heavyfetish.com/videos/12345/sample/")
	apexHdr := CookieHeaderForURL(jar, apexURL)
	if !bytes.Contains([]byte(apexHdr), []byte("session_id=sess_999")) {
		t.Errorf("expected host-only session_id=sess_999 on apex domain, got %q", apexHdr)
	}
	if _, exists := cookieMap["expired_cookie"]; exists {
		t.Error("expected expired_cookie to be rejected")
	}
	if _, exists := cookieMap["evil_token"]; exists {
		t.Error("cookie isolation failure: evil_token attached to targetURL")
	}
	if _, exists := cookieMap["SID"]; exists {
		t.Error("cookie isolation failure: google SID attached to targetURL")
	}

	if evilCookies := jar.Cookies(evilURL); len(evilCookies) != 0 {
		t.Errorf("expected 0 cookies for evil.com in target-scoped jar, got %d", len(evilCookies))
	}

	// Secure cookie must not be sent over plaintext HTTP
	insecureHdr := CookieHeaderForURL(jar, insecureTargetURL)
	if bytes.Contains([]byte(insecureHdr), []byte("bunny_shield")) {
		t.Errorf("Secure cookie bunny_shield leaked over plaintext HTTP: %q", insecureHdr)
	}
}

func encryptTestChromiumCookie(t *testing.T, version, hostKey, plaintextVal string, key []byte, includeHashPrefix bool) []byte {
	t.Helper()
	var payload []byte
	if includeHashPrefix {
		h := sha256.Sum256([]byte(hostKey))
		payload = append(payload, h[:]...)
	}
	payload = append(payload, []byte(plaintextVal)...)

	padLen := aes.BlockSize - (len(payload) % aes.BlockSize)
	payload = append(payload, bytes.Repeat([]byte{byte(padLen)}, padLen)...)

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	iv := bytes.Repeat([]byte{' '}, aes.BlockSize)
	ciphertext := make([]byte, len(payload))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, payload)

	return append([]byte(version), ciphertext...)
}

func TestChromiumAESCBCDecryptionWithDomainHash(t *testing.T) {
	key := PBKDF2SHA1([]byte("peanuts"), []byte("saltysalt"), 1, 16)
	if gotHex := hex.EncodeToString(key); gotHex != "fd621fe5a2b402539dfa147ca9272778" {
		t.Fatalf("PBKDF2SHA1 mismatch: got %s, want fd621fe5a2b402539dfa147ca9272778", gotHex)
	}

	enc := encryptTestChromiumCookie(t, "v11", ".heavyfetish.com", "shield_verified_123", key, true)

	val, ok := DecryptChromiumCookieValue(enc, ".heavyfetish.com", [][]byte{key}, true)
	if !ok || val != "shield_verified_123" {
		t.Fatalf("DecryptChromiumCookieValue failed: ok=%v, val=%q", ok, val)
	}

	// Domain-swapping tampering must fail verification under Chromium v24+ hashPrefix
	if _, ok := DecryptChromiumCookieValue(enc, ".attacker.com", [][]byte{key}, true); ok {
		t.Error("expected domain-swapped cookie to fail SHA-256 prefix check")
	}
}

func TestRedirectAndHeaderScopingSecurity(t *testing.T) {
	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	targetURL, _ := url.Parse("https://www.heavyfetish.com/videos/1/")
	opts := DownloadOptions{
		Headers: []string{
			"Cookie: cf_clearance=secret_cf; bunny_shield=secret_bs",
			"Authorization: Bearer secret_jwt",
			"X-Custom-Header: custom_val",
		},
	}

	if err := ApplyOptionsToClient(client, opts, targetURL); err != nil {
		t.Fatalf("ApplyOptionsToClient failed: %v", err)
	}

	// GHSA-v8mc-9377-rwjj: Cookie header must be moved into Jar for targetURL, not sent to third-party CDN
	if got := CookieHeaderForURL(client.Jar, targetURL); got == "" {
		t.Error("expected Cookie header from opts.Headers to be scoped to targetURL in Jar")
	}
	cdnURL, _ := url.Parse("https://cdn.thirdparty.net/video.mp4")
	if got := CookieHeaderForURL(client.Jar, cdnURL); got != "" {
		t.Errorf("expected no cookies for thirdparty CDN, got %q", got)
	}

	// Verify CheckRedirect strips Authorization & Cookie on cross-domain redirect
	reqOrigin, _ := http.NewRequest("GET", "https://www.heavyfetish.com/videos/1/", nil)
	reqCross, _ := http.NewRequest("GET", "https://cdn.thirdparty.net/video.mp4", nil)
	reqCross.Header.Set("Authorization", "Bearer secret_jwt")
	reqCross.Header.Set("Cookie", "bunny_shield=secret_bs")
	reqCross.Header.Set("X-Auth-Token", "secret_tok")

	// Use custom CheckRedirect directly (ValidateRemoteURL allows public URLs)
	if err := client.CheckRedirect(reqCross, []*http.Request{reqOrigin}); err != nil {
		t.Fatalf("CheckRedirect returned unexpected error: %v", err)
	}
	if reqCross.Header.Get("Authorization") != "" {
		t.Error("Authorization header leaked across cross-domain redirect")
	}
	if reqCross.Header.Get("Cookie") != "" {
		t.Error("Cookie header leaked across cross-domain redirect")
	}
	if reqCross.Header.Get("X-Auth-Token") != "" {
		t.Error("X-Auth-Token header leaked across cross-domain redirect")
	}

	// Verify HTTPS -> HTTP downgrade on same domain also strips sensitive headers
	reqDowngrade, _ := http.NewRequest("GET", "http://www.heavyfetish.com/videos/1/", nil)
	reqDowngrade.Header.Set("Authorization", "Bearer secret_jwt")
	if err := client.CheckRedirect(reqDowngrade, []*http.Request{reqOrigin}); err != nil {
		t.Fatalf("CheckRedirect downgrade returned unexpected error: %v", err)
	}
	if reqDowngrade.Header.Get("Authorization") != "" {
		t.Error("Authorization header leaked across HTTPS->HTTP scheme downgrade")
	}
}

func TestBrowserSQLiteExtractionIsolation(t *testing.T) {
	tmpDir := t.TempDir()
	ffDB := filepath.Join(tmpDir, "cookies.sqlite")

	futureExp := time.Now().Add(24 * time.Hour).Unix()
	setupSQL := fmt.Sprintf(`
CREATE TABLE moz_cookies (host TEXT, name TEXT, value TEXT, path TEXT, expiry INTEGER, isSecure INTEGER, isHttpOnly INTEGER);
INSERT INTO moz_cookies VALUES ('.eporner.com', 'ep_session', 'valid_ep_123', '/', %d, 1, 1);
INSERT INTO moz_cookies VALUES ('.unrelated-bank.com', 'bank_auth', 'do_not_load', '/', %d, 1, 1);
`, futureExp, futureExp)

	if _, err := runSQLiteQuery(ffDB, setupSQL); err != nil {
		t.Skipf("sqlite helper unavailable: %v", err)
	}

	jar, _ := cookiejar.New(nil)
	targetURL, _ := url.Parse("https://www.eporner.com/hd-porn/3YRUtzMcWn0/test/")
	bankURL, _ := url.Parse("https://www.unrelated-bank.com/account")

	spec := BrowserSpec{Browser: "firefox", Profile: tmpDir}
	if err := extractFirefoxCookies(jar, spec, targetURL); err != nil {
		t.Fatalf("extractFirefoxCookies failed: %v", err)
	}

	if got := CookieHeaderForURL(jar, targetURL); got != "ep_session=valid_ep_123" {
		t.Errorf("expected ep_session=valid_ep_123 for eporner.com, got %q", got)
	}
	if got := CookieHeaderForURL(jar, bankURL); got != "" {
		t.Errorf("expected bank cookies to be excluded by SQLite WHERE + domain isolation, got %q", got)
	}
	_ = os.Remove(ffDB)
}

func TestChromiumSQLiteMockAndLiveExtraction(t *testing.T) {
	tmpDir := t.TempDir()
	chromeDB := filepath.Join(tmpDir, "Cookies")

	key := PBKDF2SHA1([]byte("peanuts"), []byte("saltysalt"), 1, 16)
	enc := encryptTestChromiumCookie(t, "v10", ".heavyfetish.com", "bunny_pow_ok", key, true)
	encHex := hex.EncodeToString(enc)

	futureChromeExp := (time.Now().Add(24*time.Hour).Unix() + windowsEpochOffset) * 1_000_000
	setupSQL := fmt.Sprintf(`
CREATE TABLE meta (key TEXT, value TEXT);
INSERT INTO meta VALUES ('version', '24');
CREATE TABLE cookies (host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB, path TEXT, expires_utc INTEGER, is_secure INTEGER, is_httponly INTEGER);
INSERT INTO cookies VALUES ('.heavyfetish.com', 'bunny_shield', '', X'%s', '/', %d, 1, 1);
INSERT INTO cookies VALUES ('.other-domain.com', 'other_cookie', 'plain_other', X'', '/', %d, 1, 0);
`, encHex, futureChromeExp, futureChromeExp)

	if _, err := runSQLiteQuery(chromeDB, setupSQL); err != nil {
		t.Skipf("sqlite helper unavailable: %v", err)
	}

	jar, _ := cookiejar.New(nil)
	targetURL, _ := url.Parse("https://www.heavyfetish.com/videos/123/")
	otherURL, _ := url.Parse("https://www.other-domain.com/")

	spec := BrowserSpec{Browser: "chromium", Profile: tmpDir}
	if err := extractChromiumCookies(jar, spec, targetURL); err != nil {
		t.Fatalf("extractChromiumCookies failed: %v", err)
	}

	if got := CookieHeaderForURL(jar, targetURL); got != "bunny_shield=bunny_pow_ok" {
		t.Errorf("expected bunny_shield=bunny_pow_ok, got %q", got)
	}
	if got := CookieHeaderForURL(jar, otherURL); got != "" {
		t.Errorf("expected other-domain.com cookie to be isolated out, got %q", got)
	}
}

func TestDetectBrowserClientHeadersAcrossBrowsers(t *testing.T) {
	for _, b := range []string{"chrome", "brave", "edge", "chromium", "firefox", "safari"} {
		hdrs := detectBrowserClientHeaders(b)
		if len(hdrs) == 0 {
			t.Fatalf("expected non-empty client headers for browser %q", b)
		}
		m := make(map[string]string)
		for _, h := range hdrs {
			m[h.name] = h.value
		}
		if m["User-Agent"] == "" {
			t.Errorf("browser %q missing User-Agent", b)
		}
		switch b {
		case "brave":
			if !bytes.Contains([]byte(m["Sec-Ch-Ua"]), []byte(`"Brave"`)) {
				t.Errorf("expected Brave Sec-Ch-Ua brand, got %q", m["Sec-Ch-Ua"])
			}
		case "edge":
			if !bytes.Contains([]byte(m["User-Agent"]), []byte("Edg/")) {
				t.Errorf("expected Edge User-Agent token, got %q", m["User-Agent"])
			}
		case "firefox", "safari":
			if m["Sec-Ch-Ua"] != "" {
				t.Errorf("expected empty Sec-Ch-Ua (strip marker) for %q, got %q", b, m["Sec-Ch-Ua"])
			}
		}
	}
}

func TestSanitizeRFC6265CookieValue(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	targetURL, _ := url.Parse("https://xhamster.com/videos/test")

	setIsolatedCookie(jar, &http.Cookie{
		Name:   "g_state",
		Value:  `{"i_l":0}`,
		Path:   "/",
		Secure: true,
	}, ".xhamster.com", targetURL)

	got := CookieHeaderForURL(jar, targetURL)
	if got != "g_state={i_l:0}" {
		t.Errorf("expected sanitized cookie g_state={i_l:0}, got %q", got)
	}
}

func TestSanitizeFilenameMultiOSAndByteLength(t *testing.T) {
	cjk := strings.Repeat("日本語テスト", 25) // 150 runes, 450 UTF-8 bytes
	gotCJK := SanitizeFilename(cjk)
	if len(gotCJK) > 200 {
		t.Errorf("expected UTF-8 byte length <= 200, got %d bytes", len(gotCJK))
	}

	if got := SanitizeFilename("CON.txt"); got != "_CON.txt" {
		t.Errorf("expected Windows reserved stem CON.txt -> _CON.txt, got %q", got)
	}
	if got := SanitizeFilename(`a<b>c:d"e/f\g|h?i*j . `); got != "a_b_c_d_e_f_g_h_i_j" {
		t.Errorf("unexpected sanitized filename: %q", got)
	}
}

func TestMediaExtensionsAndMimeMappings(t *testing.T) {
	if got := DetermineExt("https://example.com/get_file/clip.mp4/?download=1", "unknown"); got != "mp4" {
		t.Errorf("DetermineExt trailing slash failed: got %q, want mp4", got)
	}
	if got := MimetypeToExt("video/x-matroska; codecs=\"avc1\""); got != "mkv" {
		t.Errorf("MimetypeToExt(video/x-matroska) = %q, want mkv", got)
	}
	if got := MimetypeToExt("application/vnd.apple.mpegurl"); got != "m3u8" {
		t.Errorf("MimetypeToExt(vnd.apple.mpegurl) = %q, want m3u8", got)
	}
	if got := SanitizeExtension("exe", "mp4"); got != ".mp4" {
		t.Errorf("SanitizeExtension(exe) = %q, want .mp4 (GHSA-79w7-vh3h-8g4j)", got)
	}
	if got := SanitizeExtension("bin", "mp4"); got != ".mp4" {
		t.Errorf("SanitizeExtension(bin) = %q, want .mp4", got)
	}
	if got := SanitizeExtension("webm", "mp4"); got != ".webm" {
		t.Errorf("SanitizeExtension(webm) = %q, want .webm", got)
	}

	hdr := make(http.Header)
	hdr.Set("Content-Disposition", `attachment; filename="video_stream.mkv"`)
	hdr.Set("Content-Type", "application/octet-stream")
	if got := DetectExtFromHeaders(hdr, "mp4"); got != "mkv" {
		t.Errorf("DetectExtFromHeaders(Content-Disposition) = %q, want mkv", got)
	}
}
