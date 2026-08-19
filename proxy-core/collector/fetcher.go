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
	// 与 file 源一致：超限报错而非静默截断，避免导入被截断的残缺订阅。
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBodySize {
		return nil, fmt.Errorf("subscription content exceeds %d bytes", maxBodySize)
	}
	return body, nil
}

// fetchLocalFile 读取 file:// URL 指向的本地订阅文件，
// 大小限制与 HTTP 源一致（8MB），防止超大文件拖垮解析。
// 分块读取并在每块之间检查 ctx 取消，避免取消操作被大文件阻塞。
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

	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	// 先用 stat 查大小：超限立即报错，并据此精准预分配，避免小文件浪费 8MB。
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > maxBodySize {
		return nil, fmt.Errorf("subscription file exceeds %d bytes", maxBodySize)
	}

	body := make([]byte, 0, info.Size())
	tmp := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, rerr := f.Read(tmp)
		if n > 0 {
			body = append(body, tmp[:n]...)
			if len(body) > maxBodySize {
				return nil, fmt.Errorf("subscription file exceeds %d bytes", maxBodySize)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, rerr
		}
	}
	return body, nil
}