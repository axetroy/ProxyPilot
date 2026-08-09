package pool

import (
	"testing"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

func TestCalculateScoreFreshSuccess(t *testing.T) {
	// 全新节点，第一次检测成功，延迟 100ms
	node := &model.ProxyNode{Protocol: model.ProtocolHTTP}
	result := model.CheckResult{OK: true, Latency: 100}
	s := CalculateScore(node, result)
	if s.Score != 78 {
		t.Fatalf("expected score 78, got %d", s.Score)
	}
	if !s.Stable {
		t.Fatal("expected stable=true for a fresh node")
	}
	if !s.Success {
		t.Fatal("expected success=true")
	}
	if s.SuccessRate != 50 {
		t.Fatalf("expected success rate 50, got %d", s.SuccessRate)
	}
}

func TestCalculateScoreWithHistory(t *testing.T) {
	// 9 次成功 1 次失败，本次成功，延迟 300ms
	node := &model.ProxyNode{
		Protocol:     model.ProtocolHTTP,
		SuccessCount: 9,
		FailCount:    1,
	}
	result := model.CheckResult{OK: true, Latency: 300}
	s := CalculateScore(node, result)
	if s.Score != 84 {
		t.Fatalf("expected score 84, got %d", s.Score)
	}
	if s.Stable {
		t.Fatal("expected stable=false with a past failure")
	}
	if s.SuccessRate != 90 {
		t.Fatalf("expected success rate 90, got %d", s.SuccessRate)
	}
}

func TestCalculateScoreFailedCheck(t *testing.T) {
	// 本次检测失败，分数应被减半
	node := &model.ProxyNode{Protocol: model.ProtocolHTTP}
	result := model.CheckResult{OK: false, Latency: 0, Error: "timeout"}
	s := CalculateScore(node, result)
	if s.Score != 29 {
		t.Fatalf("expected score 29 (58*0.5), got %d", s.Score)
	}
	if s.Success {
		t.Fatal("expected success=false")
	}
	if s.SuccessRate != 0 {
		t.Fatalf("expected success rate 0, got %d", s.SuccessRate)
	}
}

func TestCalculateScoreLatencyFallback(t *testing.T) {
	// 本次检测无延迟时回退到节点历史延迟
	node := &model.ProxyNode{
		Protocol: model.ProtocolHTTP,
		Latency:  500,
	}
	result := model.CheckResult{OK: true, Latency: 0}
	s := CalculateScore(node, result)
	// latencyScore(500) = 60
	// successRate = 1/2 = 0.5
	// stability = 100, anonymity = 80
	// score = 0.4*50 + 0.3*60 + 0.2*100 + 0.1*80 = 20+18+20+8 = 66
	if s.Score != 66 {
		t.Fatalf("expected score 66, got %d", s.Score)
	}
}

func TestCalculateScoreSocks5Anonymity(t *testing.T) {
	// SOCKS5 匿名性更高
	node := &model.ProxyNode{Protocol: model.ProtocolSOCKS5}
	result := model.CheckResult{OK: true, Latency: 50}
	s := CalculateScore(node, result)
	// successRate = 0.5, latencyScore = 100, stability = 100, anonymity = 95
	// score = 0.4*50 + 0.3*100 + 0.2*100 + 0.1*95 = 20+30+20+9.5 = 79.5 -> 80
	if s.Score != 80 {
		t.Fatalf("expected score 80, got %d", s.Score)
	}
}

func TestCalculateScoreAuthLowersAnonymity(t *testing.T) {
	// 带认证的节点匿名性降到 50
	node := &model.ProxyNode{
		Protocol: model.ProtocolSOCKS5,
		Username: "user",
		Password: "pass",
	}
	result := model.CheckResult{OK: true, Latency: 50}
	s := CalculateScore(node, result)
	// successRate = 0.5, latencyScore = 100, stability = 100, anonymity = 50
	// score = 0.4*50 + 0.3*100 + 0.2*100 + 0.1*50 = 20+30+20+5 = 75
	if s.Score != 75 {
		t.Fatalf("expected score 75, got %d", s.Score)
	}
}

func TestCalculateScoreStabilityDecay(t *testing.T) {
	// 大量失败导致稳定性衰减到下限 30
	node := &model.ProxyNode{
		Protocol:     model.ProtocolHTTP,
		SuccessCount: 0,
		FailCount:    10,
	}
	result := model.CheckResult{OK: false, Latency: 0}
	s := CalculateScore(node, result)
	if s.Stable {
		t.Fatal("expected stable=false with failures")
	}
	// decay = min(30, 10*18/(0+0.01)) = 30, stability = 70
	// score = 0 + 0.3*100 + 0.2*70 + 0.1*80 = 30+14+8 = 52, *0.5 = 26
	if s.Score != 26 {
		t.Fatalf("expected score 26, got %d", s.Score)
	}
}

func TestCalculateScoreClampHigh(t *testing.T) {
	// 完美节点：分数不应超过 100
	node := &model.ProxyNode{
		Protocol:     model.ProtocolHTTP,
		SuccessCount: 100,
		FailCount:    0,
	}
	result := model.CheckResult{OK: true, Latency: 50}
	s := CalculateScore(node, result)
	// successRate = 101/101 = 1.0, latencyScore = 100, stability = 100, anonymity = 80
	// score = 40+30+20+8 = 98
	if s.Score != 98 {
		t.Fatalf("expected score 98, got %d", s.Score)
	}
	if s.Score > 100 {
		t.Fatalf("score must not exceed 100, got %d", s.Score)
	}
}

func TestCalculateScoreClampLow(t *testing.T) {
	// 极端失败场景：分数不应低于 0
	node := &model.ProxyNode{
		Protocol:     model.ProtocolHTTP,
		SuccessCount: 0,
		FailCount:    100,
	}
	result := model.CheckResult{OK: false, Latency: 0}
	s := CalculateScore(node, result)
	if s.Score < 0 {
		t.Fatalf("score must not be negative, got %d", s.Score)
	}
}

func TestLatencyScoreBuckets(t *testing.T) {
	cases := []struct {
		ms   int64
		want int
	}{
		{0, 100},
		{50, 100},
		{100, 100},
		{101, 80},
		{300, 80},
		{301, 60},
		{600, 60},
		{601, 40},
		{1000, 40},
		{1001, 20},
		{2000, 20},
		{2001, 5},
		{5000, 5},
	}
	for _, c := range cases {
		if got := latencyScore(c.ms); got != c.want {
			t.Errorf("latencyScore(%d) = %d, want %d", c.ms, got, c.want)
		}
	}
}

func TestAnonymity(t *testing.T) {
	cases := []struct {
		name string
		node *model.ProxyNode
		want int
	}{
		{"dead node", &model.ProxyNode{Status: model.StatusDead, Protocol: model.ProtocolHTTP}, 0},
		{"http", &model.ProxyNode{Status: model.StatusAlive, Protocol: model.ProtocolHTTP}, 80},
		{"https", &model.ProxyNode{Status: model.StatusAlive, Protocol: model.ProtocolHTTPS}, 80},
		{"socks5", &model.ProxyNode{Status: model.StatusAlive, Protocol: model.ProtocolSOCKS5}, 95},
		{"socks5 with auth", &model.ProxyNode{Status: model.StatusAlive, Protocol: model.ProtocolSOCKS5, Username: "u"}, 50},
		{"http with auth", &model.ProxyNode{Status: model.StatusAlive, Protocol: model.ProtocolHTTP, Username: "u"}, 50},
	}
	for _, c := range cases {
		if got := Anonymity(c.node); got != c.want {
			t.Errorf("%s: Anonymity() = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestScoreWeightsSumToOne(t *testing.T) {
	sum := WeightSuccess + WeightLatency + WeightStability + WeightAnonymity
	if sum != 1.0 {
		t.Fatalf("weights must sum to 1.0, got %v", sum)
	}
}