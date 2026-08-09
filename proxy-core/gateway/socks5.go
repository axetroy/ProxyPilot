package gateway

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"

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
		s.ln.Close()
	}
	if s.done != nil {
		<-s.done
	}
}

func (s *socksServer) handleConn(conn net.Conn) {
	defer conn.Close()

	if s.g != nil && s.g.bus != nil {
		s.g.bus.Debug(fmt.Sprintf("SOCKS5 connection accepted from %s", conn.RemoteAddr()))
	}

	buf := make([]byte, 256)
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return
	}
	if buf[0] != 0x05 {
		return
	}
	methods := int(buf[1])
	if methods > 0 {
		if _, err := io.ReadFull(conn, buf[:methods]); err != nil {
			return
		}
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	if _, err := io.ReadFull(conn, buf[:4]); err != nil {
		return
	}
	if buf[0] != 0x05 || buf[1] != 0x01 {
		_ = writeReply(conn, 0x07)
		return
	}

	target, port, err := readTarget(conn, buf)
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
	defer remote.Close()

	if err := writeReply(conn, 0x00); err != nil {
		return
	}

	copyDone := make(chan struct{})
	go func() {
		defer close(copyDone)
		_, _ = io.Copy(remote, conn)
		_ = remote.Close()
	}()
	_, _ = io.Copy(conn, remote)
	<-copyDone
	_ = conn.Close()
}

func (s *socksServer) dialTarget(ctx context.Context, target string) (net.Conn, error) {
	if s.g != nil {
		return s.g.UpstreamWithProtocol(ctx, target, model.ProtocolSOCKS5)
	}
	return net.Dial("tcp", target)
}

func readTarget(conn net.Conn, buf []byte) (string, int, error) {
	atyp := buf[3]
	var host string
	var port int

	switch atyp {
	case 0x01:
		if _, err := io.ReadFull(conn, buf[:4]); err != nil {
			return "", 0, err
		}
		host = net.IP(buf[:4]).String()
	case 0x03:
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return "", 0, err
		}
		length := int(buf[0])
		if _, err := io.ReadFull(conn, buf[:length]); err != nil {
			return "", 0, err
		}
		host = string(buf[:length])
	case 0x04:
		if _, err := io.ReadFull(conn, buf[:16]); err != nil {
			return "", 0, err
		}
		host = net.IP(buf[:16]).String()
	default:
		return "", 0, fmt.Errorf("unsupported socks address type %d", atyp)
	}

	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return "", 0, err
	}
	port = int(buf[0])<<8 | int(buf[1])
	return host, port, nil
}

func writeReply(conn net.Conn, status byte) error {
	_, err := conn.Write([]byte{0x05, status, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	return err
}
