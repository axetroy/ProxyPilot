package collector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ErrFetch is returned when the subscription content could not be retrieved.
var ErrFetch = errors.New("fetch subscription failed")

// maxBodySize 是订阅内容最大允许大小（HTTP 响应与本地文件共用）。
const maxBodySize = 8 << 20

type Fetcher struct {
	client *http.Client
}

func NewFetcher(timeout time.Duration) *Fetcher {
	return &Fetcher{
		client: &http.Client{Timeout: timeout},
	}
}

// Fetch downloads the subscription content from url.
// url 支持 http(s) 订阅地址与 file:// 本地文件，按 URL scheme 分发处理。
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return f.fetchHTTP(ctx, rawURL)
	case "file":
		return fetchLocalFile(ctx, u)
	default:
		return nil, fmt.Errorf("unsupported subscription scheme %q", u.Scheme)
	}
}

// fetchHTTP 通过 HTTP 下载订阅内容。
func (f *Fetcher) fetchHTTP(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ProxyPilot/0.1")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, ErrFetch
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, err
	}
	return body, nil
}

// fetchLocalFile 读取 file:// URL 指向的本地订阅文件，
// 大小限制与 HTTP 源一致（8MB），防止超大文件拖垮解析。
func fetchLocalFile(ctx context.Context, u *url.URL) ([]byte, error) {
	p := u.Path
	// Windows 盘符可能落在 host 字段（file://C:/xxx）：拼回路径。
	if u.Host != "" && len(u.Host) == 2 && u.Host[1] == ':' {
		p = "/" + u.Host + p
	}
	// Windows 下 file:///C:/xxx 的 Path 形如 /C:/xxx，去掉前导斜杠。
	if len(p) > 2 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}

	// 支持 ctx 取消：文件读取用 goroutine + select 兜底，避免超大文件卡住取消。
	type result struct {
		body []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		f, err := os.Open(p)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer func() { _ = f.Close() }()
		body, err := io.ReadAll(io.LimitReader(f, maxBodySize))
		done <- result{body: body, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-done:
		if r.err != nil {
			return nil, r.err
		}
		return r.body, nil
	}
}