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
