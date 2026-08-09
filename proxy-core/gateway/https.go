package gateway

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

// httpServer is a local forward HTTP proxy (handles both plain and CONNECT).
type httpServer struct {
	addr string
	g    *Gateway

	srv *http.Server
	ln  net.Listener
}

func (h *httpServer) Start() error {
	ln, err := net.Listen("tcp", h.addr)
	if err != nil {
		return err
	}
	h.ln = ln
	h.srv = &http.Server{Handler: h}
	go func() { _ = h.srv.Serve(ln) }()
	return nil
}

func (h *httpServer) Stop() {
	if h.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = h.srv.Shutdown(ctx)
	}
	if h.ln != nil {
		_ = h.ln.Close()
	}
}

// ServeHTTP implements the proxy: CONNECT -> tunnel; else absolute-form forward.
func (h *httpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		h.tunnel(w, r)
		return
	}
	h.forward(w, r)
}

func (h *httpServer) tunnel(w http.ResponseWriter, r *http.Request) {
	target := r.Host
	if target == "" {
		http.Error(w, "missing Host", http.StatusBadRequest)
		return
	}
	// 显式指定 HTTP 协议，确保 CONNECT 隧道只从 HTTP/HTTPS 节点中挑选上游，
	// 避免 HTTPS 流量错误地使用 SOCKS5 节点。
	upstream, err := h.g.UpstreamWithProtocol(r.Context(), target, model.ProtocolHTTP)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = upstream.Close() }()

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer func() { _ = client.Close() }()
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	relay(client, upstream)
}