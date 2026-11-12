package shipments

import "sync"

// Hub fans shipment updates out to public tracking WebSockets in this process.
// It is deliberately small and allocation-light. In a multi-instance
// deployment the same messages would be delivered through NATS or Redis
// pub/sub; the handler boundary would stay the same.
type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan []byte]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: make(map[string]map[chan []byte]struct{})}
}

func (h *Hub) Subscribe(trackingNumber string) (<-chan []byte, func()) {
	ch := make(chan []byte, 8)

	h.mu.Lock()
	if h.subs[trackingNumber] == nil {
		h.subs[trackingNumber] = make(map[chan []byte]struct{})
	}
	h.subs[trackingNumber][ch] = struct{}{}
	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		if subs := h.subs[trackingNumber]; subs != nil {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(h.subs, trackingNumber)
			}
		}
		h.mu.Unlock()
		close(ch)
	}
	return ch, unsubscribe
}

func (h *Hub) Publish(trackingNumber string, payload []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for ch := range h.subs[trackingNumber] {
		select {
		case ch <- payload:
		default:
			// A slow client should not block status updates for everyone else;
			// it can reconnect and fetch a fresh snapshot.
		}
	}
}
