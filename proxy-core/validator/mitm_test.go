package validator

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"testing"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

// classifyTLSFailure 必须把证书校验类错误归因为中间人特征（tlsClassMitm），
// 网络/超时类错误归因为其他（tlsClassOther），否则探测会误报/漏报。
func TestClassifyTLSFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want tlsFailureClass
	}{
		{"nil", nil, tlsClassOther},
		{"timeout", fmt.Errorf("dial: i/o timeout"), tlsClassOther},
		{"connection reset", fmt.Errorf("read: connection reset by peer"), tlsClassOther},
		{"hostname mismatch", x509.HostnameError{Host: "www.example.com"}, tlsClassMitm},
		{"hostname wrapped", fmt.Errorf("handshake: %w", x509.HostnameError{Host: "www.example.com"}), tlsClassMitm},
		{"unknown authority", x509.UnknownAuthorityError{}, tlsClassMitm},
		{"certificate invalid", x509.CertificateInvalidError{Reason: x509.Expired}, tlsClassMitm},
		{"constraint violation", x509.ConstraintViolationError{}, tlsClassMitm},
		{"go1.20 verification error", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, tlsClassMitm},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyTLSFailure(c.err); got != c.want {
				t.Fatalf("classifyTLSFailure(%v) = %d, want %d", c.err, got, c.want)
			}
		})
	}
}

// DetectMITM 对连接失败/握手失败的节点不应误判为中间人：
// 这里用不可路由的地址验证「连不上 ≠ 中间人」。
func TestDetectMITMConnectionFailureIsNotMitm(t *testing.T) {
	node := &model.ProxyNode{
		Host:     "127.0.0.1",
		Port:     1, // 保留端口，必然拒绝
		Protocol: model.ProtocolHTTP,
	}
	start := time.Now()
	mitm, detail := DetectMITM(node, "www.apple.com", 2*time.Second)
	if mitm {
		t.Fatalf("unreachable node should not be flagged as MITM, detail=%q", detail)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("DetectMITM should fail fast on unreachable node")
	}
}
