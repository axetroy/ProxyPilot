package bus

import "sync"

type Event struct {
	Type    string `json:"type"`              // log | progress
	Level   string `json:"level,omitempty"`   // debug|info|warn|error
	Message string `json:"message,omitempty"`
	Current int    `json:"current,omitempty"`
	Total   int    `json:"total,omitempty"`
}

type Bus struct {
	mu      sync.RWMutex
	subs    map[chan Event]struct{}
	history []Event
	maxHist int
}

func New() *Bus {
	return &Bus{
		subs:    make(map[chan Event]struct{}),
		history: make([]Event, 0, 64),
		maxHist: 200,
	}
}

func (b *Bus) Publish(e Event) {
	b.mu.Lock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
	if b.maxHist > 0 {
		b.history = append(b.history, e)
		if len(b.history) > b.maxHist {
			b.history = b.history[len(b.history)-b.maxHist:]
		}
	}
	b.mu.Unlock()
}

func (b *Bus) Log(level, message string) {
	b.Publish(Event{Type: "log", Level: level, Message: message})
}

func (b *Bus) Debug(message string) { b.Log("debug", message) }
func (b *Bus) Info(message string)  { b.Log("info", message) }
func (b *Bus) Warn(message string)  { b.Log("warn", message) }
func (b *Bus) Error(message string) { b.Log("error", message) }

func (b *Bus) Progress(current, total int) {
	b.Publish(Event{Type: "progress", Message: "checking", Current: current, Total: total})
}

func (b *Bus) Subscribe() chan Event {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	hist := make([]Event, len(b.history))
	copy(hist, b.history)
	b.mu.Unlock()
	// 回放历史时使用非阻塞发送：历史可能超过缓冲容量（64），
	// 如果阻塞发送，Subscribe 将永远无法返回，导致订阅者（WebSocket）卡死。
	// 缓冲满时直接丢弃旧历史，保证新连接能立即开始接收实时事件。
	for _, e := range hist {
		select {
		case ch <- e:
		default:
		}
	}
	return ch
}

func (b *Bus) Unsubscribe(ch chan Event) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
	close(ch)
}