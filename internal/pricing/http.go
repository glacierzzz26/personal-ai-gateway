package pricing

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// fetchTimeout 单页抓取总超时。官方文档页偶发慢,给足余量。
const fetchTimeout = 20 * time.Second

// maxBodyBytes 正文上限,防止误抓大文件。
const maxBodyBytes = 8 << 20

// userAgent 固定 UA(issue 要求:固定 UA)。声明用途便于对方识别与联系。
const userAgent = "personal-ai-gateway-pricing/1.0 (+self-hosted model price sync)"

// allowedHostError 非白名单 host 的拒绝错误。
type allowedHostError struct{ host string }

func (e *allowedHostError) Error() string {
	return fmt.Sprintf("域名不在官方白名单内,拒绝抓取: %s", e.host)
}

// hostAllowlist 传输层强制的域名白名单。结构性保证「只采信官方域名」——
// 即便解析器写错 URL 或重定向到第三方,也发不出去。
type hostAllowlist struct {
	base  http.RoundTripper
	hosts map[string]bool
}

func (t *hostAllowlist) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.hosts[req.URL.Hostname()] {
		return nil, &allowedHostError{host: req.URL.Hostname()}
	}
	return t.base.RoundTrip(req)
}

// AllowlistClient 用给定 base Transport 构造一个只允许官方域名的 client。
// base 通常来自 proxy.Relay.Client(即已应用代理/跳过 TLS/响应头超时)。
func AllowlistClient(base http.Client, hosts []string) *http.Client {
	h := make(map[string]bool, len(hosts))
	for _, x := range hosts {
		h[x] = true
	}
	tr := base.Transport
	if tr == nil {
		tr = http.DefaultTransport
	}
	out := base // 复制值,不共享 Transport
	out.Transport = &hostAllowlist{base: tr, hosts: h}
	out.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// 重定向同样受白名单约束(hostAllowlist 兜底),但显式限制跳数。
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return nil
	}
	return &out
}

// fetchPage 抓取官方页面,返回正文与内容 sha256(留证)。
// 只接受 2xx;非 2xx 视为失败(不解析错误页)。
func fetchPage(ctx context.Context, client *http.Client, s scraper) ([]byte, string, error) {
	fctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(fctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Encoding", "gzip")

	// 客户端按白名单构造时已带 hostAllowlist;此处再显式核对一次,双保险。
	if !hostAllowed(s, req.URL.Hostname()) {
		return nil, "", &allowedHostError{host: req.URL.Hostname()}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("抓取官方页面失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("官方页面返回 %s", resp.Status)
	}

	var r io.Reader = io.LimitReader(resp.Body, maxBodyBytes)
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, "", fmt.Errorf("解压官方页面失败: %w", err)
		}
		defer gz.Close()
		r = gz
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, "", fmt.Errorf("读取官方页面失败: %w", err)
	}
	sum := sha256.Sum256(body)
	return body, hex.EncodeToString(sum[:]), nil
}

func hostAllowed(s scraper, host string) bool {
	for _, h := range s.Hosts {
		if h == host {
			return true
		}
	}
	return false
}
