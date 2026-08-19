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
	// stability = 100, safety = 80
	// score = 0.4*50 + 0.3*60 + 0.2*100 + 0.1*80 = 20+18+20+8 = 66
	if s.Score != 66 {
		t.Fatalf("expected score 66, got %d", s.Score)
	}
}

func TestCalculateScoreSocks5Safety(t *testing.T) {
	// SOCKS5 连接安全更高
	node := &model.ProxyNode{Protocol: model.ProtocolSOCKS5}
	result := model.CheckResult{OK: true, Latency: 50}
	s := CalculateScore(node, result)
	// successRate = 0.5, latencyScore = 100, stability = 100, safety = 95
	// score = 0.4*50 + 0.3*100 + 0.2*100 + 0.1*95 = 20+30+20+9.5 = 79.5 -> 80
	if s.Score != 80 {
		t.Fatalf("expected score 80, got %d", s.Score)
	}
}

func TestCalculateScoreAuthLowersSafety(t *testing.T) {
	// 带认证的节点连接安全降到 50
	node := &model.ProxyNode{
		Protocol: model.ProtocolSOCKS5,
		Username: "user",
		Password: "pass",
	}
	result := model.CheckResult{OK: true, Latency: 50}
	s := CalculateScore(node, result)
	// successRate = 0.5, latencyScore = 100, stability = 100, safety = 50
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
	// successRate = 101/101 = 1.0, latencyScore = 100, stability = 100, safety = 80
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

func TestSafety(t *testing.T) {
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
		if got := Safety(c.node); got != c.want {
			t.Errorf("%s: Safety() = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestScoreWeightsSumToOne(t *testing.T) {
	sum := WeightSuccess + WeightLatency + WeightStability + WeightSafety
	if sum != 1.0 {
		t.Fatalf("weights must sum to 1.0, got %v", sum)
	}
}

func TestBreakdownAliveNode(t *testing.T) {
	// 存活节点：9 次成功 1 次失败，延迟 387ms
	node := &model.ProxyNode{
		Protocol:     model.ProtocolSOCKS5,
		Status:       model.StatusAlive,
		SuccessCount: 9,
		FailCount:    1,
		Latency:      387,
	}
	b := Breakdown(node)
	if b == nil {
		t.Fatal("expected non-nil breakdown")
	}
	// successRate = (9+1)/(10+1) = 90.9 -> 90
	if b.SuccessRate != 90 {
		t.Fatalf("expected successRate 90, got %d", b.SuccessRate)
	}
	// latencyScore(387) = 60
	if b.LatencyScore != 60 {
		t.Fatalf("expected latencyScore 60, got %d", b.LatencyScore)
	}
	// decay = min(30, 1*15/0.919) = 16.3 -> stability = 83
	if b.Stability != 83 {
		t.Fatalf("expected stability 83, got %d", b.Stability)
	}
	// socks5 连接安全 95
	if b.Safety != 95 {
		t.Fatalf("expected safety 95, got %d", b.Safety)
	}
	// score = 0.4*90.9 + 0.3*60 + 0.2*83 + 0.1*95 = 80.46 -> 80
	if b.Score != 80 {
		t.Fatalf("expected score 80, got %d", b.Score)
	}
	if b.WeightSuccess != 0.40 || b.WeightLatency != 0.30 || b.WeightStability != 0.20 || b.WeightSafety != 0.10 {
		t.Fatalf("unexpected weights: %v %v %v %v", b.WeightSuccess, b.WeightLatency, b.WeightStability, b.WeightSafety)
	}
}

func TestBreakdownDeadNodeHalved(t *testing.T) {
	// 死亡节点：分数减半，且与 CalculateScore 失败检测口径一致
	node := &model.ProxyNode{
		Protocol:     model.ProtocolHTTP,
		Status:       model.StatusDead,
		SuccessCount: 0,
		FailCount:    1,
		Latency:      0,
	}
	b := Breakdown(node)
	if b == nil {
		t.Fatal("expected non-nil breakdown")
	}
	// successRate = 0/1 = 0（死亡节点不把成功计入）
	if b.SuccessRate != 0 {
		t.Fatalf("expected successRate 0, got %d", b.SuccessRate)
	}
	// latencyScore(0) = 100
	if b.LatencyScore != 100 {
		t.Fatalf("expected latencyScore 100, got %d", b.LatencyScore)
	}
	// decay = min(30, 1*18/0.01) = 30 -> stability = 70
	if b.Stability != 70 {
		t.Fatalf("expected stability 70, got %d", b.Stability)
	}
	// http 连接安全 80
	if b.Safety != 80 {
		t.Fatalf("expected safety 80, got %d", b.Safety)
	}
	// score = (0 + 0.3*100 + 0.2*70 + 0.1*80) * 0.5 = (30+14+8)*0.5 = 26
	if b.Score != 26 {
		t.Fatalf("expected score 26, got %d", b.Score)
	}
}

func TestBreakdownFreshNode(t *testing.T) {
	// 全新节点（无历史）：与 CalculateScore 首次成功口径一致
	node := &model.ProxyNode{
		Protocol: model.ProtocolHTTP,
		Status:   model.StatusAlive,
		Latency:  100,
	}
	b := Breakdown(node)
	if b == nil {
		t.Fatal("expected non-nil breakdown")
	}
	// successRate = 1/2 = 50
	if b.SuccessRate != 50 {
		t.Fatalf("expected successRate 50, got %d", b.SuccessRate)
	}
	// score = 0.4*50 + 0.3*100 + 0.2*100 + 0.1*80 = 20+30+20+8 = 78
	if b.Score != 78 {
		t.Fatalf("expected score 78, got %d", b.Score)
	}
}

// ---------- calculateSafety 多维连接安全 ----------

func TestCalculateSafetyNilProbeFallback(t *testing.T) {
	// probe 为 nil（探测失败/未启用）时回退启发式
	cases := []struct {
		name string
		node *model.ProxyNode
		want int
	}{
		{"http", &model.ProxyNode{Protocol: model.ProtocolHTTP}, 80},
		{"https", &model.ProxyNode{Protocol: model.ProtocolHTTPS}, 80},
		{"socks5", &model.ProxyNode{Protocol: model.ProtocolSOCKS5}, 95},
		{"http with auth", &model.ProxyNode{Protocol: model.ProtocolHTTP, Username: "u"}, 50},
		{"socks5 with auth", &model.ProxyNode{Protocol: model.ProtocolSOCKS5, Username: "u"}, 50},
	}
	for _, c := range cases {
		got, detail := calculateSafety(c.node, nil)
		if got != c.want {
			t.Errorf("%s: calculateSafety = %d, want %d", c.name, got, c.want)
		}
		if detail != nil {
			t.Errorf("%s: detail = %v, want nil", c.name, detail)
		}
	}
}

func TestCalculateSafetySourceIPHidden(t *testing.T) {
	// 代理出口 IP 与直连不同：源 IP 隐藏，满分
	node := &model.ProxyNode{Protocol: model.ProtocolHTTP}
	probe := &model.SafetyProbe{DirectIP: "1.1.1.1", ProxiedIP: "2.2.2.2"}
	got, detail := calculateSafety(node, probe)
	if got != 100 {
		t.Fatalf("score = %d, want 100", got)
	}
	if detail == nil || detail.SourceIpHidden == nil || !*detail.SourceIpHidden {
		t.Fatalf("SourceIpHidden = %v, want true", detail.SourceIpHidden)
	}
}

func TestCalculateSafetySourceIPTransparent(t *testing.T) {
	// 代理出口 IP 与直连相同：透明代理，源 IP 隐藏得 0 分
	node := &model.ProxyNode{Protocol: model.ProtocolHTTP}
	probe := &model.SafetyProbe{DirectIP: "1.1.1.1", ProxiedIP: "1.1.1.1"}
	got, detail := calculateSafety(node, probe)
	// 0.4*0 + 0.3*100 + 0.3*100 = 60
	if got != 60 {
		t.Fatalf("score = %d, want 60", got)
	}
	if detail == nil || detail.SourceIpHidden == nil || *detail.SourceIpHidden {
		t.Fatalf("SourceIpHidden = %v, want false", detail.SourceIpHidden)
	}
}

func TestCalculateSafetyNoIPCompare(t *testing.T) {
	// 直连或代理侧 IP 缺失：无法对比，取中间值 50
	node := &model.ProxyNode{Protocol: model.ProtocolHTTP}
	probe := &model.SafetyProbe{DirectIP: "", ProxiedIP: "2.2.2.2"}
	got, detail := calculateSafety(node, probe)
	// 0.4*50 + 0.3*100 + 0.3*100 = 80
	if got != 80 {
		t.Fatalf("score = %d, want 80", got)
	}
	if detail == nil || detail.SourceIpHidden != nil {
		t.Fatalf("SourceIpHidden = %v, want nil", detail.SourceIpHidden)
	}
}

func TestCalculateSafetyHeaderLeaks(t *testing.T) {
	// 头泄漏：每项扣 30
	node := &model.ProxyNode{Protocol: model.ProtocolHTTP}
	probe := &model.SafetyProbe{
		DirectIP:    "1.1.1.1",
		ProxiedIP:   "2.2.2.2",
		HeaderLeaks: []string{"X-Forwarded-For: 10.0.0.1", "X-Real-IP: 10.0.0.2"},
	}
	got, detail := calculateSafety(node, probe)
	// 0.4*100 + 0.3*(100-60) + 0.3*100 = 40+12+30 = 82
	if got != 82 {
		t.Fatalf("score = %d, want 82", got)
	}
	if detail == nil || len(detail.HeaderLeaks) != 2 {
		t.Fatalf("HeaderLeaks = %v, want 2 items", detail.HeaderLeaks)
	}
}

func TestCalculateSafetyProxyMarkers(t *testing.T) {
	// 代理特征：每项扣 25
	node := &model.ProxyNode{Protocol: model.ProtocolHTTP}
	probe := &model.SafetyProbe{
		DirectIP:     "1.1.1.1",
		ProxiedIP:    "2.2.2.2",
		ProxyMarkers: []string{"Via: 1.1 squid", "X-Via: proxy"},
	}
	got, detail := calculateSafety(node, probe)
	// 0.4*100 + 0.3*100 + 0.3*(100-50) = 40+30+15 = 85
	if got != 85 {
		t.Fatalf("score = %d, want 85", got)
	}
	if detail == nil || len(detail.ProxyMarkers) != 2 {
		t.Fatalf("ProxyMarkers = %v, want 2 items", detail.ProxyMarkers)
	}
}

func TestCalculateSafetyCombinedPenalty(t *testing.T) {
	// 透明代理 + 头泄漏 + 代理特征：分数应被压到很低但不低于 0
	node := &model.ProxyNode{Protocol: model.ProtocolHTTP}
	probe := &model.SafetyProbe{
		DirectIP:     "1.1.1.1",
		ProxiedIP:    "1.1.1.1",
		HeaderLeaks:  []string{"X-Forwarded-For: 10.0.0.1"},
		ProxyMarkers: []string{"Via: 1.1 squid", "X-Via: proxy", "Proxy-Agent: squid"},
	}
	got, _ := calculateSafety(node, probe)
	// 0.4*0 + 0.3*(100-30) + 0.3*(100-75) = 0+21+7.5 = 28.5 -> 29
	if got != 29 {
		t.Fatalf("score = %d, want 29", got)
	}
}

func TestCalculateSafetyRotatingIP(t *testing.T) {
	// 出口 IP 轮换：基础分 100（源 IP 隐藏且无泄漏）应封顶 100，RotatingIP 标记为 true
	node := &model.ProxyNode{Protocol: model.ProtocolHTTP}
	probe := &model.SafetyProbe{
		DirectIP:   "1.1.1.1",
		ProxiedIP:  "2.2.2.2",
		ProxiedIP2: "3.3.3.3",
	}
	got, detail := calculateSafety(node, probe)
	if got != 100 {
		t.Fatalf("score = %d, want 100 (bonus clamped)", got)
	}
	if detail == nil || !detail.RotatingIP {
		t.Fatal("RotatingIP = false, want true")
	}
}

func TestCalculateSafetyRotatingIPBonus(t *testing.T) {
	// 非满分场景：头泄漏扣分后 +5 轮换奖励应生效
	node := &model.ProxyNode{Protocol: model.ProtocolHTTP}
	probe := &model.SafetyProbe{
		DirectIP:    "1.1.1.1",
		ProxiedIP:   "2.2.2.2",
		ProxiedIP2:  "3.3.3.3",
		HeaderLeaks: []string{"X-Forwarded-For: 10.0.0.1"},
	}
	got, detail := calculateSafety(node, probe)
	// 0.4*100 + 0.3*70 + 0.3*100 = 40+21+30 = 91；+5 = 96
	if got != 96 {
		t.Fatalf("score = %d, want 96", got)
	}
	if detail == nil || !detail.RotatingIP {
		t.Fatal("RotatingIP = false, want true")
	}
}

func TestCalculateSafetyNoRotating(t *testing.T) {
	// 两次经代理采样出口 IP 相同（固定出口）：不加分也不扣分，RotatingIP 为 false
	node := &model.ProxyNode{Protocol: model.ProtocolHTTP}
	probe := &model.SafetyProbe{
		DirectIP:   "1.1.1.1",
		ProxiedIP:  "2.2.2.2",
		ProxiedIP2: "2.2.2.2",
	}
	got, detail := calculateSafety(node, probe)
	// 0.4*100 + 0.3*100 + 0.3*100 = 100
	if got != 100 {
		t.Fatalf("score = %d, want 100", got)
	}
	if detail == nil || detail.RotatingIP {
		t.Fatal("RotatingIP = true, want false")
	}
}

func TestCalculateSafetyReqIssues(t *testing.T) {
	// 请求被代理改写：每项连接信息问题扣 10
	node := &model.ProxyNode{Protocol: model.ProtocolHTTP}
	probe := &model.SafetyProbe{
		DirectIP:   "1.1.1.1",
		ProxiedIP:  "2.2.2.2",
		ReqIssues:  []string{"回显端收到的请求 URL 与目标不一致: http://evil.example/x", "回显端收到的 Host 头与目标不一致: evil.example"},
		ProxiedIP2: "2.2.2.2",
	}
	got, detail := calculateSafety(node, probe)
	// 0.4*100 + 0.3*100 + 0.3*100 = 100；-2*10 = 80
	if got != 80 {
		t.Fatalf("score = %d, want 80", got)
	}
	if detail == nil || len(detail.ReqIssues) != 2 {
		t.Fatalf("ReqIssues = %v, want 2 items", detail.ReqIssues)
	}
	if detail.RotatingIP {
		t.Fatal("RotatingIP = true, want false")
	}
}

func TestCalculateScoreUsesProbeSafety(t *testing.T) {
	// CalculateScore 应使用真实探测结果而非启发式：
	// 透明代理（IP 相同）连接安全 60，总分应低于启发式 80 的版本
	node := &model.ProxyNode{Protocol: model.ProtocolHTTP}
	result := model.CheckResult{
		OK:      true,
		Latency: 100,
		Safety: &model.SafetyProbe{
			DirectIP:  "1.1.1.1",
			ProxiedIP: "1.1.1.1",
		},
	}
	s := CalculateScore(node, result)
	// successRate = 0.5, latencyScore = 100, stability = 100, safety = 60
	// score = 0.4*50 + 0.3*100 + 0.2*100 + 0.1*60 = 20+30+20+6 = 76
	if s.Score != 76 {
		t.Fatalf("expected score 76, got %d", s.Score)
	}
	if s.SafetyDetail == nil {
		t.Fatal("expected SafetyDetail to be set")
	}
	if s.SafetyDetail.Score != 60 {
		t.Fatalf("SafetyDetail.Score = %d, want 60", s.SafetyDetail.Score)
	}
}

func TestBreakdownUsesSafetyDetail(t *testing.T) {
	// Breakdown 优先使用节点上的探测明细，保证明细与总分一致
	node := &model.ProxyNode{
		Protocol: model.ProtocolHTTP,
		Status:   model.StatusAlive,
		Latency:  100,
		SafetyDetail: &model.SafetyDetail{
			SourceIpHidden: boolPtr(false),
			Score:          60,
		},
	}
	b := Breakdown(node)
	if b == nil {
		t.Fatal("expected non-nil breakdown")
	}
	if b.Safety != 60 {
		t.Fatalf("expected safety 60 (from detail), got %d", b.Safety)
	}
	// score = 0.4*50 + 0.3*100 + 0.2*100 + 0.1*60 = 76
	if b.Score != 76 {
		t.Fatalf("expected score 76, got %d", b.Score)
	}
}

func boolPtr(v bool) *bool { return &v }
