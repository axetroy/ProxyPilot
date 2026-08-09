package gateway

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"golang.org/x/net/proxy"
)

func TestSOCKS5ProxyRoundTrip(t *testing.T) {
	targetListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen target: %v", err)
	}
	defer targetListener.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := targetListener.Accept()
		if err != nil {
			return
		}
		accepted <- conn
	}()

	server := &socksServer{addr: "127.0.0.1:0"}
	if err := server.Start(); err != nil {
		t.Fatalf("start socks proxy: %v", err)
	}
	defer server.Stop()

	dialer, err := proxy.SOCKS5("tcp", server.ln.Addr().String(), nil, proxy.Direct)
	if err != nil {
		t.Fatalf("build socks5 dialer: %v", err)
	}

	conn, err := dialer.Dial("tcp", targetListener.Addr().String())
	if err != nil {
		t.Fatalf("dial through socks proxy: %v", err)
	}
	defer conn.Close()

	remoteConn := <-accepted
	defer remoteConn.Close()

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write to proxied conn: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(remoteConn, buf); err != nil {
		t.Fatalf("read from remote side: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("unexpected payload %q", string(buf))
	}

	if _, err := remoteConn.Write([]byte("pong")); err != nil {
		t.Fatalf("write to remote side: %v", err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatalf("read proxied response: %v", err)
	}
	if string(response) != "pong" {
		t.Fatalf("unexpected response %q", string(response))
	}
}

func TestSOCKS5ProxyTLSHandshake(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	socks := &socksServer{addr: "127.0.0.1:0"}
	if err := socks.Start(); err != nil {
		t.Fatalf("start socks proxy: %v", err)
	}
	defer socks.Stop()

	dialer, err := proxy.SOCKS5("tcp", socks.ln.Addr().String(), nil, proxy.Direct)
	if err != nil {
		t.Fatalf("build socks5 dialer: %v", err)
	}

	conn, err := dialer.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial through socks proxy: %v", err)
	}
	defer conn.Close()

	tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, ServerName: "example.com"})
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("tls handshake through SOCKS5 failed: %v", err)
	}
	defer tlsConn.Close()
}

func TestSOCKS5ProxyHTTPClientHTTPS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	socks := &socksServer{addr: "127.0.0.1:0"}
	if err := socks.Start(); err != nil {
		t.Fatalf("start socks proxy: %v", err)
	}
	defer socks.Stop()

	proxyURL, err := url.Parse("socks5://" + socks.ln.Addr().String())
	if err != nil {
		t.Fatalf("parse socks5 proxy url: %v", err)
	}

	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: transport}

	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("http client over SOCKS5 failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if string(body) != "ok" {
		t.Fatalf("unexpected body %q", string(body))
	}
}
