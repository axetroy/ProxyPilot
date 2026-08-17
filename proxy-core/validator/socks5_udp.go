package validator

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

// SOCKS5 命令码。
const (
	cmdConnect      = 0x01
	cmdUDPAssociate = 0x03
)

// SOCKS5 地址类型。
const (
	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04
)

// maxUDPDatagram 是 SOCKS5 UDP 数据报的最大缓冲（UDP 数据报上限 65535 字节，
// 含 10 字节 SOCKS5 头，净负载不超过 65525）。
const maxUDPDatagram = 64 * 1024

// ParseSOCKS5UDP 解析 SOCKS5 UDP 数据报，返回目标主机、端口与负载。
// 数据报格式：RSV(2) | FRAG(1) | ATYP(1) | DST.ADDR | DST.PORT | DATA。
// 不支持分片（FRAG != 0 直接报错，由调用方丢弃）。
func ParseSOCKS5UDP(dgram []byte) (host string, port int, payload []byte, err error) {
	if len(dgram) < 4 {
		return "", 0, nil, io.ErrUnexpectedEOF
	}
	if dgram[0] != 0 || dgram[1] != 0 {
		return "", 0, nil, fmt.Errorf("invalid RSV in socks5 udp datagram")
	}
	if frag := dgram[2]; frag != 0 {
		return "", 0, nil, fmt.Errorf("socks5 udp fragmentation not supported (FRAG=%d)", frag)
	}
	off := 4
	switch atyp := dgram[3]; atyp {
	case atypIPv4:
		if len(dgram) < off+4+2 {
			return "", 0, nil, io.ErrUnexpectedEOF
		}
		host = net.IP(dgram[off : off+4]).String()
		off += 4
	case atypDomain:
		if len(dgram) < off+1 {
			return "", 0, nil, io.ErrUnexpectedEOF
		}
		n := int(dgram[off])
		off++
		if len(dgram) < off+n+2 {
			return "", 0, nil, io.ErrUnexpectedEOF
		}
		host = string(dgram[off : off+n])
		off += n
	case atypIPv6:
		if len(dgram) < off+16+2 {
			return "", 0, nil, io.ErrUnexpectedEOF
		}
		host = net.IP(dgram[off : off+16]).String()
		off += 16
	default:
		return "", 0, nil, fmt.Errorf("unsupported socks5 address type %d", dgram[3])
	}
	if len(dgram) < off+2 {
		return "", 0, nil, io.ErrUnexpectedEOF
	}
	port = int(binary.BigEndian.Uint16(dgram[off:]))
	off += 2
	return host, port, dgram[off:], nil
}

// BuildSOCKS5UDP 构造 SOCKS5 UDP 数据报（RSV=0、FRAG=0）。
// 目标地址可为 IP（IPv4/IPv6）或域名，域名长度必须 1-255。
func BuildSOCKS5UDP(host string, port int, payload []byte) ([]byte, error) {
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("invalid udp port %d", port)
	}
	var atyp byte
	var addr []byte
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			atyp = atypIPv4
			addr = ip4
		} else {
			atyp = atypIPv6
			addr = ip.To16()
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			return nil, fmt.Errorf("invalid socks5 udp domain %q", host)
		}
		atyp = atypDomain
		addr = []byte{byte(len(host))}
		addr = append(addr, []byte(host)...)
	}
	dgram := make([]byte, 0, 4+len(addr)+2+len(payload))
	dgram = append(dgram, 0x00, 0x00, 0x00, atyp)
	dgram = append(dgram, addr...)
	dgram = append(dgram, byte(port>>8), byte(port))
	dgram = append(dgram, payload...)
	return dgram, nil
}

// UDPSession 是通过上游 SOCKS5 节点建立的 UDP 中继通道。
// Send 把目标与负载封装成 SOCKS5 UDP 数据报发往上游中继；
// Receive 读取上游中继转发的数据报，返回真实目标地址与负载。
// 生命周期由上层管理：不使用时必须 Close 释放控制连接与 UDP socket。
type UDPSession struct {
	ctrl      net.Conn
	udp       *net.UDPConn
	relayAddr *net.UDPAddr
}

// UDPAssociate 与 SOCKS5 代理节点建立 UDP 中继（CMD=0x03）。
// 节点协议必须是 SOCKS5：HTTP/HTTPS 节点只支持 CONNECT 隧道，无法承载 UDP。
// 握手成功后返回可收发的 UDPSession；调用方负责 Close。
func UDPAssociate(node *model.ProxyNode, timeout time.Duration) (*UDPSession, error) {
	if node == nil {
		return nil, fmt.Errorf("nil proxy node")
	}
	if node.Protocol != model.ProtocolSOCKS5 {
		return nil, fmt.Errorf("udp associate requires a socks5 node, got %q", node.Protocol)
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	proxyAddr := net.JoinHostPort(node.Host, strconv.Itoa(node.Port))
	ctrl, err := net.DialTimeout("tcp", proxyAddr, timeout)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = ctrl.Close()
		}
	}()

	// 握手阶段设置截止时间，避免阻塞在无响应的上游节点上。
	if err := ctrl.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}

	// 1. 方法协商：优先无认证；节点带认证信息时同时声明 USERNAME/PASSWORD。
	methods := []byte{0x00}
	if node.Username != "" {
		methods = append(methods, 0x02)
	}
	if _, err := ctrl.Write(append([]byte{0x05, byte(len(methods))}, methods...)); err != nil {
		return nil, err
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(ctrl, resp); err != nil {
		return nil, err
	}
	if resp[0] != 0x05 {
		return nil, fmt.Errorf("bad socks5 version %d from %s", resp[0], proxyAddr)
	}
	switch resp[1] {
	case 0x00: // 无认证
	case 0x02: // USERNAME/PASSWORD
		if node.Username == "" {
			return nil, fmt.Errorf("upstream %s requires socks5 authentication", proxyAddr)
		}
		if err := writeSocks5UserPass(ctrl, node.Username, node.Password); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(ctrl, resp); err != nil {
			return nil, err
		}
		if resp[1] != 0x00 {
			return nil, fmt.Errorf("socks5 authentication failed: code %d", resp[1])
		}
	case 0xFF:
		return nil, fmt.Errorf("upstream %s has no acceptable auth method", proxyAddr)
	default:
		return nil, fmt.Errorf("unsupported socks5 auth method %d from %s", resp[1], proxyAddr)
	}

	// 2. 发送 UDP ASSOCIATE 请求（DST.ADDR 用 0.0.0.0:0，由上游分配中继地址）。
	if _, err := ctrl.Write([]byte{0x05, cmdUDPAssociate, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0}); err != nil {
		return nil, err
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(ctrl, head); err != nil {
		return nil, err
	}
	if head[0] != 0x05 {
		return nil, fmt.Errorf("bad socks5 version %d in associate reply", head[0])
	}
	if head[1] != 0x00 {
		return nil, fmt.Errorf("socks5 udp associate failed: code %d", head[1])
	}
	host, port, err := readSOCKS5Addr(ctrl, head[3])
	if err != nil {
		return nil, err
	}
	relayAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}

	// 3. 建立与上游中继通信的 UDP socket，仅绑定本机回环，避免对外暴露。
	localIP := net.IPv4(127, 0, 0, 1)
	if relayAddr.IP != nil && relayAddr.IP.To4() == nil {
		localIP = net.IPv6loopback
	}
	udp, err := net.DialUDP("udp", &net.UDPAddr{IP: localIP}, relayAddr)
	if err != nil {
		return nil, err
	}

	// 握手完成，取消截止时间；会话期间不设超时，生命周期由上层管理。
	if err := ctrl.SetDeadline(time.Time{}); err != nil {
		_ = udp.Close()
		return nil, err
	}
	ok = true
	return &UDPSession{ctrl: ctrl, udp: udp, relayAddr: relayAddr}, nil
}

// Send 把目标主机/端口与负载封装成 SOCKS5 UDP 数据报发往上游中继。
func (s *UDPSession) Send(host string, port int, payload []byte) error {
	dgram, err := BuildSOCKS5UDP(host, port, payload)
	if err != nil {
		return err
	}
	_, err = s.udp.Write(dgram)
	return err
}

// Receive 读取上游中继转发的数据报，返回真实目标地址与负载。
func (s *UDPSession) Receive() (host string, port int, payload []byte, err error) {
	buf := make([]byte, maxUDPDatagram)
	n, err := s.udp.Read(buf)
	if err != nil {
		return "", 0, nil, err
	}
	return ParseSOCKS5UDP(buf[:n])
}

// Close 关闭 UDP 通道与控制连接，释放会话。
func (s *UDPSession) Close() error {
	_ = s.udp.Close()
	return s.ctrl.Close()
}

// writeSocks5UserPass 发送 SOCKS5 USERNAME/PASSWORD 认证子协商。
func writeSocks5UserPass(conn net.Conn, user, pass string) error {
	if len(user) > 255 || len(pass) > 255 {
		return fmt.Errorf("socks5 credentials too long")
	}
	buf := []byte{0x01, byte(len(user))}
	buf = append(buf, []byte(user)...)
	buf = append(buf, byte(len(pass)))
	buf = append(buf, []byte(pass)...)
	_, err := conn.Write(buf)
	return err
}

// readSOCKS5Addr 从连接读取一个 SOCKS5 地址（ATYP + ADDR + PORT）。
func readSOCKS5Addr(r io.Reader, atyp byte) (string, int, error) {
	var host string
	switch atyp {
	case atypIPv4:
		b := make([]byte, 4)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	case atypDomain:
		b := make([]byte, 1)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		n := int(b[0])
		if n == 0 {
			return "", 0, fmt.Errorf("empty socks5 domain")
		}
		b = make([]byte, n)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = string(b)
	case atypIPv6:
		b := make([]byte, 16)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	default:
		return "", 0, fmt.Errorf("unsupported socks5 address type %d", atyp)
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(r, pb); err != nil {
		return "", 0, err
	}
	port := int(binary.BigEndian.Uint16(pb))
	return host, port, nil
}
