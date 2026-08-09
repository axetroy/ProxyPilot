package bus

import (
	"testing"
	"time"
)

func TestPublishAndReceive(t *testing.T) {
	b := New()
	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	b.Info("hello")
	select {
	case e := <-ch:
		if e.Type != "log" || e.Level != "info" || e.Message != "hello" {
			t.Fatalf("unexpected event: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestSubscribeReplaysHistory(t *testing.T) {
	b := New()
	b.Debug("d1")
	b.Warn("w1")
	b.Error("e1")

	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	// 历史应被回放（顺序无关紧要，但内容应齐全）
	seen := map[string]bool{}
	deadline := time.After(time.Second)
	for len(seen) < 3 {
		select {
		case e := <-ch:
			seen[e.Message] = true
		case <-deadline:
			t.Fatalf("timed out, seen %v", seen)
		}
	}
	for _, msg := range []string{"d1", "w1", "e1"} {
		if !seen[msg] {
			t.Fatalf("missing history event %q", msg)
		}
	}
}

func TestHistoryBounded(t *testing.T) {
	b := New()
	for i := 0; i < b.maxHist+50; i++ {
		b.Info("msg")
	}
	if len(b.history) != b.maxHist {
		t.Fatalf("expected history capped at %d, got %d", b.maxHist, len(b.history))
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	b := New()
	ch := b.Subscribe()
	b.Unsubscribe(ch)

	// 已关闭的 channel 不应再收到事件
	b.Info("after-unsubscribe")
	select {
	case e, ok := <-ch:
		if ok {
			t.Fatalf("expected channel closed, got event %+v", e)
		}
	default:
		// channel 已关闭且为空，正常
	}
}

func TestPublishNonBlocking(t *testing.T) {
	b := New()
	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	// 填满缓冲后 Publish 不应阻塞
	for i := 0; i < cap(ch)+10; i++ {
		b.Info("flood")
	}
	// 若 Publish 阻塞，测试会卡死；能走到这里即通过
}

func TestProgressEvent(t *testing.T) {
	b := New()
	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	b.Progress(3, 10)
	select {
	case e := <-ch:
		if e.Type != "progress" || e.Current != 3 || e.Total != 10 {
			t.Fatalf("unexpected progress event: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for progress event")
	}
}

func TestMultipleSubscribers(t *testing.T) {
	b := New()
	ch1 := b.Subscribe()
	ch2 := b.Subscribe()
	defer b.Unsubscribe(ch1)
	defer b.Unsubscribe(ch2)

	b.Info("broadcast")
	for i, ch := range []chan Event{ch1, ch2} {
		select {
		case e := <-ch:
			if e.Message != "broadcast" {
				t.Fatalf("subscriber %d got wrong event: %+v", i, e)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d timed out", i)
		}
	}
}

func TestLogHelpers(t *testing.T) {
	b := New()
	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	b.Debug("d")
	b.Info("i")
	b.Warn("w")
	b.Error("e")

	levels := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for len(levels) < 4 {
		select {
		case e := <-ch:
			levels[e.Level] = true
		case <-deadline:
			t.Fatalf("timed out, levels %v", levels)
		}
	}
	for _, lvl := range []string{"debug", "info", "warn", "error"} {
		if !levels[lvl] {
			t.Fatalf("missing level %q", lvl)
		}
	}
}
