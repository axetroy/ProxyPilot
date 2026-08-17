package gateway

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"

	"github.com/axetroy/ProxyPilot/proxy-core/validator"
)

// errNoSOCKS5UDP 表示节点池中没有可用的 SOCKS5 存活节点。
// UDP 中继必须经 SOCKS5 节点承载：HTTP/HTTPS 节点只支持 CONNECT 隧道，无法转发 UDP。
var errNoSOCKS5UDP = errors.New("no live SOCKS5 node available for UDP relay")

// udpBackend 抽象本地 UDP 中继把数据报送达真正目标的方式：
// 直连模式直接把负载发往目标地址；经代理模式先与上游 SOCKS5 节点建立 UDP 中继。
type udpBackend interface {
	// Send 把目标主机/端口与负载发送到上游（经代理时封装成 SOCKS5 UDP 数据报）。
	Send(host string, port int, payload []byte) error
	// Receive 读取上游转发的数据报，返回真实目标地址与负载。
	Receive() (host string, port int, payload []byte, err error)
	Close() error
}

// directUDPBackend 直接把负载发往目标 UDP 地址（网关未配置节点池时的直连模式）。
type directUDPBackend struct {
	udp *net.UDPConn
}

func (b *directUDPBackend) Send(host string, port int, payload []byte) error {
	addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	_, err = b.udp.WriteToUDP(payload, addr)
	return err
}

func (b *directUDPBackend) Receive() (host string, port int, payload []byte, err error) {
	buf := make([]byte, 64*1024)
	n, addr, err := b.udp.ReadFromUDP(buf)
	if err != nil {
		return "", 0, nil, err
	}
	return addr.IP.String(), addr.Port, buf[:n], nil
}

func (b *directUDPBackend) Close() error {
	return b.udp.Close()
}

// upstreamSOCKS5UDP 是经上游 SOCKS5 节点建立的 UDP 中继 backend。
type upstreamSOCKS5UDP struct {
	sess *validator.UDPSession
}

func (b *upstreamSOCKS5UDP) Send(host string, port int, payload []byte) error {
	return b.sess.Send(host, port, payload)
}

func (b *upstreamSOCKS5UDP) Receive() (host string, port int, payload []byte, err error) {
	return b.sess.Receive()
}

func (b *upstreamSOCKS5UDP) Close() error {
	return b.sess.Close()
}

// udpBackend 选择上游 backend：网关无节点池时直连，否则经 SOCKS5 节点建立 UDP 中继。
func (s *socksServer) udpBackend() (udpBackend, error) {
	if s.g == nil {
		udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			return nil, err
		}
		return &directUDPBackend{udp: udp}, nil
	}
	return s.g.NewUDPRelay()
}

// handleUDPAssociate 处理 SOCKS5 UDP ASSOCIATE（CMD=0x03）：
//  1. 在本地监听一个 UDP 中继端口，通过 REPLY 把地址告知客户端；
//  2. 选择 SOCKS5 上游节点建立 UDP 中继，把客户端数据报转发给上游；
//  3. 上游返回的数据报重新封装后发回本地客户端。
//
// 本地没有存活的 SOCKS5 节点时返回 0x07（命令不支持）：HTTP/HTTPS 节点
// 无法承载 UDP 流量，不提供跨协议回退。
// 会话随控制连接关闭而结束（符合 RFC 1928：客户端应保持控制连接）。
func (s *socksServer) handleUDPAssociate(conn net.Conn, br *bufio.Reader, buf []byte) {
	// ASSOCIATE 请求的 DST.ADDR/DST.PORT 通常为 0.0.0.0:0；
	// 若客户端显式指定了 UDP 源地址，后续回包优先发往该地址。
	reqHost, reqPort, err := readTarget(br, buf)
	if err != nil {
		_ = writeReply(conn, 0x01)
		return
	}

	// 本地 UDP 中继：每个会话独立监听，仅绑定本机回环。
	local, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		_ = writeReply(conn, 0x01)
		return
	}

	// 建立上游 UDP 中继（必须走 SOCKS5 节点）。
	backend, err := s.udpBackend()
	if err != nil {
		_ = local.Close()
		if errors.Is(err, errNoSOCKS5UDP) {
			_ = writeReply(conn, 0x07)
		} else {
			_ = writeReply(conn, 0x01)
		}
		return
	}

	// 通知客户端本地中继地址。
	if err := writeReplyWithAddr(conn, local.LocalAddr().(*net.UDPAddr)); err != nil {
		_ = local.Close()
		_ = backend.Close()
		return
	}

	var (
		clientMu sync.Mutex
		client   *net.UDPAddr
	)
	if reqHost != "0.0.0.0" && reqHost != "::" && reqPort != 0 {
		if ip := net.ParseIP(reqHost); ip != nil {
			client = &net.UDPAddr{IP: ip, Port: reqPort}
		}
	}

	var wg sync.WaitGroup
	// 会话结束：关闭本地中继与上游，让各读循环退出。
	var closeOnce sync.Once
	shutdown := func() {
		closeOnce.Do(func() {
			_ = local.Close()
			_ = backend.Close()
			_ = conn.Close()
		})
	}
	defer func() {
		shutdown()
		wg.Wait()
	}()

	// 监听控制连接：客户端结束会话（关闭连接）时触发 shutdown。
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(io.Discard, conn)
		shutdown()
	}()

	// 上游回包 -> 本地客户端（按 SOCKS5 UDP 数据报封装回发）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			host, port, payload, err := backend.Receive()
			if err != nil {
				return
			}
			clientMu.Lock()
			addr := client
			clientMu.Unlock()
			if addr == nil {
				continue
			}
			dgram, err := validator.BuildSOCKS5UDP(host, port, payload)
			if err != nil {
				continue
			}
			_, _ = local.WriteToUDP(dgram, addr)
		}
	}()

	// 本地客户端 -> 上游：解析数据报并转发。首个数据报的源地址记为客户端。
	readBuf := make([]byte, 64*1024)
	for {
		n, from, err := local.ReadFromUDP(readBuf)
		if err != nil {
			return
		}
		clientMu.Lock()
		client = from
		clientMu.Unlock()

		host, port, payload, err := validator.ParseSOCKS5UDP(readBuf[:n])
		if err != nil {
			continue
		}
		if err := backend.Send(host, port, payload); err != nil {
			return
		}
	}
}

// writeReplyWithAddr 发送 SOCKS5 成功响应，BND.ADDR/BND.PORT 为给定 UDP 地址。
func writeReplyWithAddr(conn net.Conn, addr *net.UDPAddr) error {
	reply := []byte{0x05, 0x00, 0x00}
	if ip4 := addr.IP.To4(); ip4 != nil {
		reply = append(reply, 0x01)
		reply = append(reply, ip4...)
	} else if ip16 := addr.IP.To16(); ip16 != nil {
		reply = append(reply, 0x04)
		reply = append(reply, ip16...)
	} else {
		return fmt.Errorf("unsupported local address %s", addr)
	}
	reply = append(reply, byte(addr.Port>>8), byte(addr.Port))
	_, err := conn.Write(reply)
	return err
}
