package gateway

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

// forward relays a plain HTTP proxy request (absolute-form) to the upstream.
func (h *httpServer) forward(w http.ResponseWriter, r *http.Request) {
	if !r.URL.IsAbs() {
		http.Error(w, "proxy requires absolute-form request", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// 复用网关共享的 Transport（由 NewGateway 创建），避免每个请求新建/销毁；
	// 仅当网关未走 NewGateway 构造（如测试）时兜底创建。
	transport := h.g.forwardTransport
	if transport == nil {
		transport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				// addr is the upstream target host:port; connect through the node pool.
				// 显式指定 HTTP 协议，确保只从 HTTP/HTTPS 节点中挑选上游，
				// 避免 HTTP 流量错误地使用 SOCKS5 节点。
				return h.g.UpstreamWithProtocol(ctx, addr, model.ProtocolHTTP)
			},
			DisableKeepAlives: true,
		}
	}

	req := r.Clone(ctx)
	req.RequestURI = ""
	if host := req.Header.Get("Host"); host != "" {
		req.Host = host
	}
	if req.URL.Scheme == "" {
		req.URL.Scheme = "http"
	}
	removeHopByHop(req.Header)

	resp, err := transport.RoundTrip(req)
	if err != nil {
		// 只向客户端返回通用错误，避免泄露内部节点信息（节点地址/凭据等）。
		if h.g.bus != nil {
			h.g.bus.Debug(fmt.Sprintf("forward %s failed: %v", req.URL, err))
		}
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	removeHopByHop(resp.Header)
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func relay(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(b, a); _ = b.Close(); done <- struct{}{} }()
	go func() { _, _ = io.Copy(a, b); _ = a.Close(); done <- struct{}{} }()
	<-done
	<-done
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

var hopByHop = []string{
	"Connection", "Proxy-Connection", "Keep-Alive",
	"Proxy-Authenticate", "Proxy-Authorization",
	"TE", "Trailer", "Transfer-Encoding", "Upgrade",
}

func removeHopByHop(h http.Header) {
	for _, hdr := range hopByHop {
		h.Del(hdr)
	}
	for _, c := range strings.Split(h.Get("Connection"), ",") {
		h.Del(strings.TrimSpace(c))
	}
}
