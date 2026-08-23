package validator

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

// SpeedTest 经 node 代理对 target 做带宽测速，返回字节/秒速率。
// 测速通过「反复请求 target 并累计下载字节」实现：
//   - 若 target 返回大体积内容（如专用测速文件），一次流式下载即可获得准确速率；
//   - 若 target 仅返回小页面，则复用同一 keep-alive 连接多次取样，得到该节点的真实吞吐采样。
//
// 无论哪种情况，只要 target 经节点可达（推荐留空复用检测目标 check_target），
// 就不会因目标不可达而卡到超时。sampleBytes 为采样目标字节数（默认 3MiB）。
func SpeedTest(node *model.ProxyNode, target string, timeout time.Duration, sampleBytes int64) (int64, error) {
	if sampleBytes <= 0 {
		sampleBytes = 3 << 20
	}
	if target == "" {
		return 0, fmt.Errorf("speed test target 为空")
	}
	u, err := url.Parse(target)
	if err != nil {
		return 0, fmt.Errorf("speed test target 非法: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	transport := &http.Transport{
		DialContext: (&net.Dialer{Timeout: timeout}).DialContext,
		// 启用 keep-alive：小页面多轮取样时复用同一连接，仅承担 RTT 开销。
		MaxIdleConns:        1,
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
		// 响应头超时较宽，容许慢速节点先返回头再慢慢吐数据，
		// 但整段下载受外层 timeout 兜底，不会无限挂起。
		ResponseHeaderTimeout: 15 * time.Second,
		// https 代理跳过证书校验：与连通性检测一致，聚焦「传输速率」而非证书合法性。
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	transport.Proxy = func(*http.Request) (*url.URL, error) { return httpProxyURL(node) }

	client := &http.Client{Transport: transport, Timeout: timeout}

	start := time.Now()
	deadline := start.Add(timeout)
	total := int64(0)
	const maxIters = 1024
	for i := 0; i < maxIters; i++ {
		if total >= sampleBytes || time.Now().After(deadline) {
			break
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("User-Agent", "ProxyPilot/0.1")
		resp, err := client.Do(req)
		if err != nil {
			// 已采到部分样本：按已用时间折算速率返回，避免完全失败（更贴近真实吞吐）。
			if total > 0 {
				if elapsed := time.Since(start); elapsed > 0 {
					return int64(float64(total) / elapsed.Seconds()), nil
				}
			}
			return 0, err
		}
		remaining := sampleBytes - total
		n, _ := io.Copy(io.Discard, io.LimitReader(resp.Body, remaining))
		_ = resp.Body.Close()
		total += n
		if n == 0 {
			break // 目标返回空响应，无法继续累积
		}
	}
	elapsed := time.Since(start)
	if elapsed <= 0 || total <= 0 {
		return 0, fmt.Errorf("speed test 未产出有效速率")
	}
	speed := int64(float64(total) / elapsed.Seconds())
	if speed < 0 {
		speed = 0
	}
	return speed, nil
}
