package gateway

import (
	"bufio"
	"net"
	"sync"
)

// mixedServer 在单一端口上同时提供 HTTP 与 SOCKS5 代理。
// 通过首字节嗅探区分协议：SOCKS5 握手固定以 0x05 开头，
// HTTP 请求则以 ASCII 方法名（GET/POST/CONNECT...）开头，二者不会混淆。
type mixedServer struct {
	addr string
	g    *Gateway

	ln   net.Listener
	done chan struct{}

	http  *httpServer
	socks *socksServer

	httpCh chan net.Conn // 分派给 http.Server 的 HTTP 连接
}

func (m *mixedServer) Start() error {
	ln, err := net.Listen("tcp", m.addr)
	if err != nil {
		return err
	}

	m.ln = ln
	m.done = make(chan struct{})
	m.httpCh = make(chan net.Conn, 64)

	m.socks = &socksServer{g: m.g}
	m.http = &httpServer{g: m.g}
	// http.Server 从 channelListener 消费已嗅探为 HTTP 的连接
	m.http.serveOn(newConnListener(ln.Addr(), m.httpCh))

	go m.acceptLoop()
	return nil
}

func (m *mixedServer) acceptLoop() {
	defer close(m.done)
	for {
		conn, err := m.ln.Accept()
		if err != nil {
			return
		}
		go m.dispatch(conn)
	}
}

// dispatch 根据连接首字节将连接分派给 SOCKS5 或 HTTP 处理器。
func (m *mixedServer) dispatch(conn net.Conn) {
	br := bufio.NewReader(conn)
	b, err := br.Peek(1)
	if err != nil {
		_ = conn.Close()
		return
	}
	if b[0] == 0x05 {
		m.socks.handleConnWithReader(conn, br)
		return
	}
	// 其余视为 HTTP，交给 http.Server 处理（peekedConn 保留已缓冲的字节）
	select {
	case m.httpCh <- &peekedConn{Conn: conn, r: br}:
	default:
		_ = conn.Close() // 连接过多，拒绝以避免阻塞嗅探循环
	}
}

func (m *mixedServer) Stop() {
	if m.ln != nil {
		_ = m.ln.Close() // 停止 accept，acceptLoop 随后退出并关闭 done
	}
	if m.http != nil {
		m.http.Stop()
	}
	if m.done != nil {
		<-m.done
	}
}

// peekedConn 包装已嗅探的连接，Read 优先消费 bufio 中已缓冲的字节，
// 避免 http.Server 读丢嗅探阶段的数据。
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

// connListener 是一个由外部推送连接的监听器，供 http.Server 消费分派后的连接。
type connListener struct {
	addr net.Addr
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
}

func newConnListener(addr net.Addr, ch chan net.Conn) *connListener {
	return &connListener{addr: addr, ch: ch, done: make(chan struct{})}
}

func (l *connListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *connListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *connListener) Addr() net.Addr { return l.addr }
