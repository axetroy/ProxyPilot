package validator

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

// DefaultMITMProbeHost 中间人探测的默认目标域名：
// 选择公信 CA 签发、全球可达且极少被目标侧干扰的站点，
// 保证「直连基线」在大多数网络环境下都能拿到真证书。
const DefaultMITMProbeHost = "www.apple.com"

// tlsFailureClass 对 TLS 握手失败的归因分类。
type tlsFailureClass int

const (
	// tlsClassOther 网络类失败（超时/重置/拒绝等），无法归因为中间人。
	tlsClassOther tlsFailureClass = iota
	// tlsClassMitm 证书校验类失败（主机名不匹配 / 非公信 CA / 证书无效），
	// 是中间人劫持的典型特征，可进入直连基线复核。
	tlsClassMitm
)

// DetectMITM 探测节点是否存在 HTTPS 中间人（MITM）行为。
//
// 原理：正常代理对 HTTPS 只做 TCP 隧道，客户端拿到的应是目标站点的真证书；
// 若代理做中间人，则会向客户端返回一张伪造证书（CN/SAN 不匹配或非公信 CA 签发）。
// 因此先经节点与 probeHost:443 完成 TLS 握手（开启标准证书校验），再直连同目标握手作为基线：
//   - 经节点校验失败且失败类型属于证书问题，同时直连校验通过 → 判定中间人；
//   - 直连也失败（本机环境异常/DNS 污染/被墙）→ 无法定罪，不标记（避免误报）；
//   - 经节点是网络类失败（超时/重置）→ 属可用性问题，交由连通性检测处理，不标记。
//
// 返回是否中间人及原因描述（供错误日志展示）。函数只读，不做任何标记。
func DetectMITM(node *model.ProxyNode, probeHost string, timeout time.Duration) (bool, string) {
	if probeHost == "" {
		probeHost = DefaultMITMProbeHost
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	addr := net.JoinHostPort(probeHost, "443")

	// 第一步：经节点握手。连接失败属可用性问题，不算中间人。
	conn, err := ConnectTCP(node, addr, timeout)
	if err != nil {
		return false, ""
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	tlsConn := tls.Client(conn, &tls.Config{ServerName: probeHost})
	handshakeErr := tlsConn.Handshake()
	_ = tlsConn.Close()
	if handshakeErr == nil {
		return false, ""
	}
	if classifyTLSFailure(handshakeErr) != tlsClassMitm {
		return false, ""
	}

	// 第二步：直连基线复核。只有直连能拿到合法证书时才能把
	// 「经节点的证书异常」归因于节点本身，排除本机环境因素。
	directConn, err := tls.DialWithDialer(&net.Dialer{Timeout: timeout}, "tcp", addr,
		&tls.Config{ServerName: probeHost})
	if err != nil {
		return false, ""
	}
	_ = directConn.Close()

	return true, fmt.Sprintf("经节点访问 %s 返回无法通过校验的证书：%v", probeHost, handshakeErr)
}

// classifyTLSFailure 归因 TLS 握手失败：证书校验类错误返回 tlsClassMitm，
// 网络/超时等返回 tlsClassOther。兼容 Go 1.20+ 的 tls.CertificateVerificationError 包装。
func classifyTLSFailure(err error) tlsFailureClass {
	if err == nil {
		return tlsClassOther
	}
	var hostnameErr x509.HostnameError
	if errors.As(err, &hostnameErr) {
		return tlsClassMitm
	}
	var authorityErr x509.UnknownAuthorityError
	if errors.As(err, &authorityErr) {
		return tlsClassMitm
	}
	var invalidErr x509.CertificateInvalidError
	if errors.As(err, &invalidErr) {
		return tlsClassMitm
	}
	var constraintErr x509.ConstraintViolationError
	if errors.As(err, &constraintErr) {
		return tlsClassMitm
	}
	// Go 1.20+ 会把校验错误包装在 CertificateVerificationError 里，
	// 递归检查其内部错误以保持分类稳定。
	var verifyErr *tls.CertificateVerificationError
	if errors.As(err, &verifyErr) {
		return classifyTLSFailure(verifyErr.Err)
	}
	return tlsClassOther
}
