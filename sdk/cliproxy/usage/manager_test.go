package usage

import (
	"context"
	"testing"
	"time"
	"unsafe"
)

type blockingUsagePlugin struct {
	started chan Record
	release chan struct{}
}

func (p *blockingUsagePlugin) HandleUsage(ctx context.Context, record Record) {
	p.started <- record
	<-p.release
}

func TestManagerDropsNewestRecordWhenQueueIsFull(t *testing.T) {
	manager := NewManager(1)
	plugin := &blockingUsagePlugin{
		started: make(chan Record, 2),
		release: make(chan struct{}),
	}
	manager.Register(plugin)
	t.Cleanup(func() {
		close(plugin.release)
		manager.Stop()
	})

	manager.Publish(context.Background(), Record{Model: "active"})
	select {
	case record := <-plugin.started:
		if record.Model != "active" {
			t.Fatalf("active record model = %q, want active", record.Model)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for active record dispatch")
	}

	manager.Publish(context.Background(), Record{Model: "queued"})
	manager.Publish(context.Background(), Record{Model: "dropped"})

	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.queue) != 1 {
		t.Fatalf("queue len = %d, want 1", len(manager.queue))
	}
	if manager.queue[0].record.Model != "queued" {
		t.Fatalf("queued record model = %q, want queued", manager.queue[0].record.Model)
	}
}

func TestManagerClearsConsumedQueueSlot(t *testing.T) {
	manager := NewManager(2)
	plugin := &blockingUsagePlugin{
		started: make(chan Record, 2),
		release: make(chan struct{}),
	}
	manager.Register(plugin)

	firstCtx := context.WithValue(context.Background(), struct{}{}, "first")
	manager.queue = append(manager.queue,
		queueItem{ctx: firstCtx, record: Record{Model: "first"}},
		queueItem{ctx: context.Background(), record: Record{Model: "second"}},
	)

	go manager.run(context.Background())
	select {
	case record := <-plugin.started:
		if record.Model != "first" {
			t.Fatalf("first dispatched model = %q, want first", record.Model)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first record dispatch")
	}

	manager.mu.Lock()
	if len(manager.queue) != 1 {
		manager.mu.Unlock()
		t.Fatalf("queue len after first dispatch = %d, want 1", len(manager.queue))
	}
	previous := previousQueueItem(&manager.queue[0])
	if previous.ctx != nil || previous.record.Model != "" {
		manager.mu.Unlock()
		t.Fatalf("consumed queue slot still retains ctx or record: ctx=%v model=%q", previous.ctx, previous.record.Model)
	}
	manager.closed = true
	manager.mu.Unlock()

	manager.cond.Broadcast()
	close(plugin.release)
}

func previousQueueItem(item *queueItem) *queueItem {
	return (*queueItem)(unsafe.Add(unsafe.Pointer(item), -int(unsafe.Sizeof(queueItem{}))))
}
