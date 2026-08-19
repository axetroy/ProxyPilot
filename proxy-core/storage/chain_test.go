package storage

import (
	"testing"
)

// TestChainCRUD 链路创建/查询/更新/开关/删除的完整流程。
func TestChainCRUD(t *testing.T) {
	st, err := New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	defer func() { _ = st.Close() }()

	// 初始为空
	chains, err := st.ListChains()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(chains) != 0 {
		t.Fatalf("initial chains = %d, want 0", len(chains))
	}

	// 创建
	c, err := st.CreateChain("双层", []int64{1, 2})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.ID <= 0 || c.Name != "双层" || len(c.NodeIDs) != 2 || c.NodeIDs[0] != 1 || c.NodeIDs[1] != 2 {
		t.Fatalf("created chain = %+v", c)
	}
	if c.Enabled {
		t.Fatal("new chain should be disabled by default")
	}

	// 列表
	chains, err = st.ListChains()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(chains) != 1 {
		t.Fatalf("chains = %d, want 1", len(chains))
	}

	// 按 ID 查询
	got, err := st.GetChain(c.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %+v %v", got, err)
	}

	// 更新：改名称与节点列表，并启用
	newIDs := []int64{2, 1, 3}
	enabled := true
	if err := st.UpdateChain(c.ID, "三层", newIDs, &enabled); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = st.GetChain(c.ID)
	if got.Name != "三层" || len(got.NodeIDs) != 3 || got.NodeIDs[0] != 2 || !got.Enabled {
		t.Fatalf("updated chain = %+v", got)
	}

	// 仅开关（name/nodeIDs 为空时不影响其他字段）
	disabled := false
	if err := st.SetChainEnabled(c.ID, disabled); err != nil {
		t.Fatalf("set enabled: %v", err)
	}
	got, _ = st.GetChain(c.ID)
	if got.Enabled {
		t.Fatal("chain should be disabled after SetChainEnabled(false)")
	}
	if got.Name != "三层" {
		t.Fatalf("name changed unintentionally: %s", got.Name)
	}

	// 删除
	if err := st.DeleteChain(c.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err = st.GetChain(c.ID)
	if err != nil || got != nil {
		t.Fatalf("get after delete = %+v %v, want nil", got, err)
	}
	if err := st.DeleteChain(c.ID); err == nil {
		t.Fatal("deleting non-existent chain should error")
	}
}

// TestChainHealth 健康检测字段的写入与自动停用流程：
// UpdateChainHealth 记录结果，SetChainAutoDisabled 停用并保留失败原因，
// 手动启用（UpdateChain/SetChainEnabled）重置自动停用标记与连续失败计数。
func TestChainHealth(t *testing.T) {
	st, err := New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	defer func() { _ = st.Close() }()

	c, err := st.CreateChain("c1", []int64{1, 2})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// 初始健康字段为零值
	if c.LastCheckedAt != nil || c.LastOK || c.LastLatency != 0 || c.LastError != "" || c.ConsecutiveFailures != 0 || c.AutoDisabled {
		t.Fatalf("initial health = %+v, want zero", c)
	}

	// 记录一次成功：清空错误与连续失败
	if err := st.UpdateChainHealth(c.ID, true, 123, "", 0); err != nil {
		t.Fatalf("update health: %v", err)
	}
	got, _ := st.GetChain(c.ID)
	if got.LastCheckedAt == nil || !got.LastOK || got.LastLatency != 123 || got.LastError != "" || got.ConsecutiveFailures != 0 || got.AutoDisabled {
		t.Fatalf("health after success = %+v", got)
	}

	// 记录失败：累加连续失败次数
	if err := st.UpdateChainHealth(c.ID, false, 0, "dial timeout", 1); err != nil {
		t.Fatalf("update health: %v", err)
	}
	got, _ = st.GetChain(c.ID)
	if got.LastOK || got.LastError != "dial timeout" || got.ConsecutiveFailures != 1 {
		t.Fatalf("health after failure = %+v", got)
	}

	// 自动停用：标记 auto_disabled，保留失败原因，连续失败归零
	if err := st.SetChainAutoDisabled(c.ID); err != nil {
		t.Fatalf("auto disable: %v", err)
	}
	got, _ = st.GetChain(c.ID)
	if got.Enabled || !got.AutoDisabled || got.LastError != "dial timeout" || got.ConsecutiveFailures != 0 {
		t.Fatalf("health after auto disable = %+v", got)
	}

	// 手动启用（SetChainEnabled true）：重置自动停用标记
	if err := st.SetChainEnabled(c.ID, true); err != nil {
		t.Fatalf("set enabled: %v", err)
	}
	got, _ = st.GetChain(c.ID)
	if !got.Enabled || got.AutoDisabled || got.ConsecutiveFailures != 0 {
		t.Fatalf("health after manual enable = %+v", got)
	}

	// 手动启用（UpdateChain enabled true）：同样重置
	disabled := false
	if err := st.SetChainEnabled(c.ID, disabled); err != nil {
		t.Fatalf("disable: %v", err)
	}
	enabled := true
	if err := st.UpdateChain(c.ID, "c1", nil, &enabled); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = st.GetChain(c.ID)
	if !got.Enabled || got.AutoDisabled {
		t.Fatalf("health after update-enable = %+v", got)
	}

	// 手动测试结果（RecordChainCheck）：只更新展示字段，不动连续失败计数。
	if err := st.SetChainEnabled(c.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := st.UpdateChainHealth(c.ID, false, 0, "timeout", 1); err != nil {
		t.Fatalf("update health: %v", err)
	}
	if err := st.RecordChainCheck(c.ID, true, 99, ""); err != nil {
		t.Fatalf("record check: %v", err)
	}
	got, _ = st.GetChain(c.ID)
	if !got.LastOK || got.LastLatency != 99 || got.LastError != "" || got.ConsecutiveFailures != 1 {
		t.Fatalf("health after RecordChainCheck = %+v, want updated display but keep consecutive=1", got)
	}
}
