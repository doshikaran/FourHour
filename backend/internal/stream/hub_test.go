package stream

import (
	"sync"
	"testing"
)

func TestPublishFanOutAndSlowSubscriberDropped(t *testing.T) {
	h := NewHub(2)
	fast, slow := h.Subscribe(), h.Subscribe()
	// fast drains after every publish, so it never holds more than one
	// message; this keeps the test independent of goroutine scheduling.
	var got []Message
	for i := 0; i < 5; i++ {
		if err := h.Publish("tick", map[string]int{"i": i}); err != nil {
			t.Fatal(err)
		}
		m, ok := <-fast.C
		if !ok {
			t.Fatalf("fast dropped at message %d", i)
		}
		got = append(got, m)
	}
	if len(got) != 5 || string(got[4].Data) != `{"i":4}` || got[0].Event != "tick" {
		t.Fatalf("%+v", got)
	}
	// slow never read: dropped after its buffer of 2 filled.
	n := 0
	for range slow.C {
		n++
	}
	if n != 2 || h.Len() != 1 {
		t.Fatalf("slow got %d, subs %d", n, h.Len())
	}
	h.Unsubscribe(fast)
	h.Unsubscribe(fast)
	if string(Message{Event: "x", Data: []byte(`{}`)}.Format()) != "event: x\ndata: {}\n\n" {
		t.Fatal("format")
	}
}

func TestConcurrentPublishSubscribe(t *testing.T) {
	h := NewHub(64)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			s := h.Subscribe()
			for j := 0; j < 10; j++ {
				select {
				case <-s.C:
				default:
				}
			}
			h.Unsubscribe(s)
		}()
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = h.Publish("tick", i)
			}
		}(i)
	}
	wg.Wait()
}
