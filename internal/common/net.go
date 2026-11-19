package common

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func IsDisallowedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
		// Block 0.0.0.0/8 ("This network" routes to localhost on Linux/macOS),
		// 100.64.0.0/10 (RFC 6598 CGNAT / Tailscale), 192.0.0.0/24, 198.18.0.0/15, and broadcast.
		if ip4[0] == 0 ||
			(ip4[0] == 100 && ip4[1]&0xC0 == 64) ||
			(ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 0) ||
			(ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19)) ||
			ip4.Equal(net.IPv4bcast) {
			return true
		}
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

func NewHTTPClient() (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}

	dialer := &net.Dialer{
		Timeout:   15 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   MaxThreads,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("no IP addresses found for host %s", host)
			}
			for _, ipAddr := range ips {
				if IsDisallowedIP(ipAddr.IP) {
					return nil, fmt.Errorf("refusing connection to non-public IP %s for host %s", ipAddr.IP, host)
				}
			}
			var lastErr error
			for _, ipAddr := range ips {
				target := net.JoinHostPort(ipAddr.IP.String(), port)
				conn, err := dialer.DialContext(ctx, network, target)
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}

	client := &http.Client{
		Transport: tr,
		Jar:       jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if _, err := ValidateRemoteURL(req.URL.String()); err != nil {
				return err
			}
			if len(via) > 0 {
				prev := via[len(via)-1].URL
				crossHost := !SameRegistrableDomain(prev.Hostname(), req.URL.Hostname())
				schemeDowngrade := strings.EqualFold(prev.Scheme, "https") && strings.EqualFold(req.URL.Scheme, "http")
				if crossHost || schemeDowngrade {
					req.Header.Del("Cookie")
					req.Header.Del("Authorization")
					req.Header.Del("Proxy-Authorization")
					req.Header.Del("X-Auth-Token")
					req.Header.Del("Token")
				}
			}
			return nil
		},
	}

	return client, nil
}

func ValidateRemoteURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("URL %q is missing host", rawURL)
	}
	return u, nil
}

func ResolveURL(base *url.URL, ref string) (*url.URL, error) {
	refURL, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return nil, fmt.Errorf("invalid reference URI %q: %w", ref, err)
	}
	resolved := base.ResolveReference(refURL)
	return ValidateRemoteURL(resolved.String())
}

func RetryDelay(resp *http.Response, fallback time.Duration) time.Duration {
	if resp != nil {
		if ra := strings.TrimSpace(resp.Header.Get("Retry-After")); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
				d := time.Duration(secs) * time.Second
				if d > 30*time.Second {
					return 30 * time.Second
				}
				return d
			}
		}
	}
	return fallback
}

func ShouldRetryStatus(code int) bool {
	return code == http.StatusTooManyRequests || code == http.StatusRequestTimeout || code >= 500
}

func FetchBytesWithRetry(ctx context.Context, client *http.Client, targetURL, referer string, maxBytes int64, timeout time.Duration) ([]byte, *url.URL, error) {
	waitTime := 2 * time.Second
	var lastErr error

	for attempt := 1; attempt <= MaxRetries; attempt++ {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if attempt > 1 {
			select {
			case <-time.After(waitTime):
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
			waitTime *= 2
		}

		reqCtx, cancel := context.WithTimeout(ctx, timeout)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, targetURL, nil)
		if err != nil {
			cancel()
			return nil, nil, err
		}
		req.Header.Set("User-Agent", UserAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		req.Header.Set("Sec-Ch-Ua", `"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"`)
		req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
		req.Header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
		req.Header.Set("Upgrade-Insecure-Requests", "1")
		if referer != "" {
			req.Header.Set("Referer", referer)
		}

		resp, err := client.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}

		if resp.StatusCode != http.StatusOK {
			wait := RetryDelay(resp, waitTime)
			statusErr := fmt.Errorf("HTTP %s", resp.Status)
			retry := ShouldRetryStatus(resp.StatusCode)
			resp.Body.Close()
			cancel()
			if !retry {
				return nil, nil, statusErr
			}
			lastErr = statusErr
			waitTime = wait
			continue
		}

		finalURL := resp.Request.URL
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
		resp.Body.Close()
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		if int64(len(data)) > maxBytes {
			return nil, nil, fmt.Errorf("response body exceeded maximum allowed size of %d bytes", maxBytes)
		}
		return data, finalURL, nil
	}

	return nil, nil, lastErr
}
