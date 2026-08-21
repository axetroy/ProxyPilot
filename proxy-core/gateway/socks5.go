package gateway

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

// socksServer is a local SOCKS5 proxy routed through the node pool.
type socksServer struct {
	addr string
	g    *Gateway

	ln   net.Listener
	done chan struct{}
}

func (s *socksServer) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}

	s.ln = ln
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handleConn(conn)
		}
	}()
	return nil
}

func (s *socksServer) Stop() {
	if s.ln != nil {
		_ = s.ln.Close()
	}
	if s.done != nil {
		<-s.done
	}
}

func (s *socksServer) handleConn(conn net.Conn) {
	s.handleConnWithReader(conn, bufio.NewReader(conn))
}

// handleConnWithReader 处理一条 SOCKS5 连接，握手数据从 br 读取。
// 混合模式下由 mixedServer 传入已嗅探的 bufio.Reader，避免丢失首字节。
func (s *socksServer) handleConnWithReader(conn net.Conn, br *bufio.Reader) {
	if !s.g.trackConn(conn) {
		// 并发连接超限：连接已在 trackConn 中关闭，直接结束处理。
		return
	}
	defer func() {
		s.g.untrackConn(conn)
		_ = conn.Close()
	}()

	if s.g != nil && s.g.bus != nil {
		s.g.bus.Debug(fmt.Sprintf("SOCKS5 connection accepted from %s", conn.RemoteAddr()))
	}

	// 握手阶段限时：客户端建立连接后迟迟不发握手数据（慢速攻击）时，
	// 在 socksHandshakeTimeout 后超时关闭，避免连接占住 goroutine 与 fd。
	_ = conn.SetReadDeadline(time.Now().Add(socksHandshakeTimeout))
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	buf := make([]byte, 256)
	if _, err := io.ReadFull(br, buf[:2]); err != nil {
		return
	}
	if buf[0] != 0x05 {
		return
	}
	methods := int(buf[1])
	if methods > 0 {
		if _, err := io.ReadFull(br, buf[:methods]); err != nil {
			return
		}
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	if _, err := io.ReadFull(br, buf[:4]); err != nil {
		return
	}
	if buf[0] != 0x05 {
		_ = writeReply(conn, 0x07)
		return
	}
	switch buf[1] {
	case 0x01: // CONNECT
		s.handleConnect(conn, br, buf)
	case 0x03: // UDP ASSOCIATE
		s.handleUDPAssociate(conn, br, buf)
	default:
		_ = writeReply(conn, 0x07)
	}
}

// handleConnect 处理 SOCKS5 CONNECT（CMD=0x01）：建立到目标地址的隧道并双向转发。
func (s *socksServer) handleConnect(conn net.Conn, br *bufio.Reader, buf []byte) {
	target, port, err := readTarget(br, buf)
	if err != nil {
		_ = writeReply(conn, 0x01)
		return
	}

	if s.g != nil && s.g.bus != nil {
		s.g.bus.Debug(fmt.Sprintf("SOCKS5 relay target=%s:%d from %s", target, port, conn.RemoteAddr()))
	}

	remote, err := s.dialTarget(context.Background(), net.JoinHostPort(target, strconv.Itoa(port)))
	if err != nil {
		_ = writeReply(conn, 0x04)
		return
	}
	defer func() { _ = remote.Close() }()

	if err := writeReply(conn, 0x00); err != nil {
		return
	}

	// 握手完成：清除握手阶段的读超时，后续转发由 relay 的 idle deadline 管理。
	_ = conn.SetDeadline(time.Time{})
	relay(conn, remote)
}

func (s *socksServer) dialTarget(ctx context.Context, target string) (net.Conn, error) {
	if s.g != nil {
		return s.g.UpstreamWithProtocol(ctx, target, model.ProtocolSOCKS5)
	}
	return net.Dial("tcp", target)
}

func readTarget(br *bufio.Reader, buf []byte) (string, int, error) {
	atyp := buf[3]
	var host string
	var port int

	switch atyp {
	case 0x01:
		if _, err := io.ReadFull(br, buf[:4]); err != nil {
			return "", 0, err
		}
		host = net.IP(buf[:4]).String()
	case 0x03:
		if _, err := io.ReadFull(br, buf[:1]); err != nil {
			return "", 0, err
		}
		length := int(buf[0])
		if _, err := io.ReadFull(br, buf[:length]); err != nil {
			return "", 0, err
		}
		host = string(buf[:length])
	case 0x04:
		if _, err := io.ReadFull(br, buf[:16]); err != nil {
			return "", 0, err
		}
		host = net.IP(buf[:16]).String()
	default:
		return "", 0, fmt.Errorf("unsupported socks address type %d", atyp)
	}

	if _, err := io.ReadFull(br, buf[:2]); err != nil {
		return "", 0, err
	}
	port = int(buf[0])<<8 | int(buf[1])
	return host, port, nil
}

func writeReply(conn net.Conn, status byte) error {
	_, err := conn.Write([]byte{0x05, status, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	return err
}
