package gateway

import (
	"context"
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

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// addr is the upstream target host:port; connect through the node pool.
			// 显式指定 HTTP 协议，确保只从 HTTP/HTTPS 节点中挑选上游，
			// 避免 HTTP 流量错误地使用 SOCKS5 节点。
			return h.g.UpstreamWithProtocol(ctx, addr, model.ProtocolHTTP)
		},
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()

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
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	removeHopByHop(resp.Header)
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func relay(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(b, a); b.Close(); done <- struct{}{} }()
	go func() { _, _ = io.Copy(a, b); a.Close(); done <- struct{}{} }()
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
