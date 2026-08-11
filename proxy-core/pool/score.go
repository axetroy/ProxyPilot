package pool

import (
	"math"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

// 评分权重参考 DESIGN.md 第 9 节的建议：
// - 成功率：反映节点历史上是否稳定可用，权重最高。
// - 延迟：越低越好，体现节点的响应速度。
// - 稳定性：衡量节点是否经常出现波动或间歇性失败。
// - 匿名性：表示代理类型对匿名程度的影响。
// 其中匿名性会根据协议类型做差异化处理：
// - SOCKS5 通常更适合匿名场景，默认给更高分；
// - HTTP/HTTPS 的匿名性通常更依赖是否带认证和是否透明转发，默认分数略低。
const (
	WeightSuccess   = 0.40
	WeightLatency   = 0.30
	WeightStability = 0.20
	WeightAnonymity = 0.10
)

// ScoreResult 表示一次评分结果，包含最终分数和相关状态信息。
type ScoreResult struct {
	Score       int
	Stable      bool
	Success     bool
	SuccessRate int
}

// CalculateScore 根据最新检测结果和节点历史表现，计算 0-100 的综合评分。
// 算法分为 5 步：
// 1. 先根据历史成功/失败次数计算成功率：
//   - total = 历史成功次数 + 历史失败次数
//   - 如果 total 为 0，先按 1 处理，避免除零。
//   - 如果当前检测成功，则把这次成功也计入成功率，公式为 (成功次数 + 1) / (总次数 + 1)。
//
// 2. 读取本次检测延迟；如果本次检测没有返回有效延迟，就回退到节点上一次的延迟。
// 3. 把延迟映射成 0-100 的分数：
//   - 100ms 内：100 分
//   - 300ms 内：80 分
//   - 600ms 内：60 分
//   - 1000ms 内：40 分
//   - 2000ms 内：20 分
//   - 超过 2000ms：5 分
//
// 4. 根据历史失败次数推算稳定性：
//   - 失败次数为 0 时，稳定性为 100。
//   - 如果有失败，则按失败次数和成功率进行衰减，公式为 stability = 100 - decay。
//   - decay 上限为 30，避免稳定性降得过低。
//   - 对于 SOCKS5，若当前检测成功且历史稳定，稳定性会更容易保持较高分；
//     对于 HTTP/HTTPS，则在失败偏多时会更快被打压，避免把不稳定的 HTTP 节点误判成好节点。
//
// 5. 结合权重做加权求和：
//   - 成功率权重 40%
//   - 延迟权重 30%
//   - 稳定性权重 20%
//   - 匿名性权重 10%
//   - 最终结果会被限制在 0~100 之间，并四舍五入。
//
// 6. 如果本次检测失败，则把最终分数再乘以 0.5，形成明显惩罚。
func CalculateScore(node *model.ProxyNode, result model.CheckResult) ScoreResult {
	total := node.SuccessCount + node.FailCount
	if total == 0 {
		total = 1
	}
	successRate := float64(node.SuccessCount) / float64(total)
	if result.OK {
		// 把这次成功也计入统计，避免当前成功被忽略。
		successRate = float64(node.SuccessCount+1) / float64(total+1)
	}

	latency := result.Latency
	if latency <= 0 {
		latency = node.Latency
	}
	latencyScore := latencyScore(latency)

	// 稳定性基于历史失败次数推断：失败越少，说明节点越稳定；
	// 如果出现了间歇性失败，会适度降低稳定性分数。
	// 对 SOCKS5 来说，稳定性通常更依赖长期成功率；
	// 对 HTTP/HTTPS 来说，若失败次数持续增加，会更快受到惩罚。
	stable := node.FailCount == 0
	stability := 100
	if node.FailCount > 0 {
		decayBase := 15.0
		if node.Protocol == model.ProtocolHTTP || node.Protocol == model.ProtocolHTTPS {
			decayBase = 18.0
		}
		decay := math.Min(30, float64(node.FailCount)*decayBase/float64(successRate+0.01))
		stability = int(100 - decay)
	}
	if stability < 0 {
		stability = 0
	}

	// 匿名性默认会按协议类型做差异化：
	// - SOCKS5 默认给 95，表示其匿名性通常更好；
	// - HTTP/HTTPS 默认给 80；
	// - 如果节点带有认证信息，则认为匿名性更低，进一步降到 50。
	anonymity := 80
	if node.Protocol == model.ProtocolSOCKS5 {
		anonymity = 95
	}
	if node.Username != "" {
		anonymity = 50
	}

	score := WeightSuccess*successRate*100 +
		WeightLatency*float64(latencyScore) +
		WeightStability*float64(stability) +
		WeightAnonymity*float64(anonymity)
	if !result.OK {
		score *= 0.5
	}
	return ScoreResult{
		Score:       int(math.Round(math.Min(100, math.Max(0, score)))),
		Stable:      stable,
		Success:     result.OK,
		SuccessRate: int(successRate * 100),
	}
}

// latencyScore 将延迟毫秒数映射为 0-100 的分数，延迟越低分数越高。
func latencyScore(ms int64) int {
	switch {
	case ms <= 100:
		return 100
	case ms <= 300:
		return 80
	case ms <= 600:
		return 60
	case ms <= 1000:
		return 40
	case ms <= 2000:
		return 20
	default:
		return 5
	}
}

// Anonymity 根据节点协议和状态，给出一个 0-100 的匿名性评分。
// 这里保持和 CalculateScore 的匿名性规则一致，确保选择器和评分器使用同一套口径。
func Anonymity(node *model.ProxyNode) int {
	if node.Status == model.StatusDead {
		return 0
	}
	anonymity := 80
	if node.Protocol == model.ProtocolSOCKS5 {
		anonymity = 95
	}
	if node.Username != "" {
		anonymity = 50
	}
	return anonymity
}

// Breakdown 根据节点当前状态，还原一次评分的各维度明细，供前端展示评分计算过程。
// 计算口径与 CalculateScore 完全一致（成功率、延迟分、稳定性、匿名性、权重、死亡惩罚），
// 这样前端看到的明细与列表中的总分能够对得上。
func Breakdown(node *model.ProxyNode) *model.ScoreBreakdown {
	total := node.SuccessCount + node.FailCount
	if total == 0 {
		total = 1
	}
	successRate := float64(node.SuccessCount) / float64(total)
	if node.Status == model.StatusAlive {
		// 存活节点视为最近一次检测成功，把这次成功也计入统计。
		successRate = float64(node.SuccessCount+1) / float64(total+1)
	}

	latencyScore := latencyScore(node.Latency)

	stability := 100
	if node.FailCount > 0 {
		decayBase := 15.0
		if node.Protocol == model.ProtocolHTTP || node.Protocol == model.ProtocolHTTPS {
			decayBase = 18.0
		}
		decay := math.Min(30, float64(node.FailCount)*decayBase/float64(successRate+0.01))
		stability = int(100 - decay)
	}
	if stability < 0 {
		stability = 0
	}

	anonymity := 80
	if node.Protocol == model.ProtocolSOCKS5 {
		anonymity = 95
	}
	if node.Username != "" {
		anonymity = 50
	}

	score := WeightSuccess*successRate*100 +
		WeightLatency*float64(latencyScore) +
		WeightStability*float64(stability) +
		WeightAnonymity*float64(anonymity)
	if node.Status == model.StatusDead {
		// 死亡节点与检测失败一致，最终分数减半。
		score *= 0.5
	}

	return &model.ScoreBreakdown{
		SuccessRate:     int(successRate * 100),
		LatencyScore:    latencyScore,
		Stability:       stability,
		Anonymity:       anonymity,
		WeightSuccess:   WeightSuccess,
		WeightLatency:   WeightLatency,
		WeightStability: WeightStability,
		WeightAnonymity: WeightAnonymity,
		Score:           int(math.Round(math.Min(100, math.Max(0, score)))),
	}
}
