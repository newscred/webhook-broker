package dispatcher

import (
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/newscred/webhook-broker/storage/data"
	"github.com/stretchr/testify/assert"
)

func TestPriorityQueueBasic(t *testing.T) {
	rand.Seed(time.Now().Unix())
	pq := NewJobPriorityQueue(counter.QueuedJobCount)
	priorities := rand.Perm(100)

	for _, priority := range priorities {
		messagePayload := strconv.Itoa(priority)
		msg, _ := data.NewMessage(channel, producer, messagePayload, "application/json", data.HeadersMap{})
		dj := &data.DeliveryJob{Message: msg}
		pq.Enqueue(&Job{Data: dj, Priority: uint(priority)})
	}
	for expectedPriority := 99; expectedPriority >= 0; expectedPriority-- {
		assert.Equal(t, pq.Len(), expectedPriority+1)
		topJob := pq.Dequeue()
		assert.Equal(t, uint(expectedPriority), topJob.Priority)
		assert.Equal(t, strconv.Itoa(expectedPriority), topJob.Data.Message.Payload)
	}
}

func TestPriorityQueueEqualPriorityIsFIFO(t *testing.T) {
	pq := NewJobPriorityQueue(counter.QueuedJobCount)
	for seq := uint64(1); seq <= 50; seq++ {
		msg, _ := data.NewMessage(channel, producer, strconv.FormatUint(seq, 10), "application/json", data.HeadersMap{})
		pq.Enqueue(&Job{Data: &data.DeliveryJob{Message: msg}, Priority: 1, seq: seq})
	}
	for seq := uint64(1); seq <= 50; seq++ {
		assert.Equal(t, strconv.FormatUint(seq, 10), pq.Dequeue().Data.Message.Payload)
	}
}

func BenchmarkPriorityQueue(b *testing.B) {

	for i := 0; i < b.N; i++ {
		stressPriorityQueue()
	}
}

func stressPriorityQueue() {
	MaxQueueSize := 100000
	contentType := "application/json"
	pq := NewJobPriorityQueue(counter.QueuedJobCount)
	for i := 0; i < MaxQueueSize; i++ {
		priority := i % 3
		messagePayload := strconv.Itoa(priority)
		msg, _ := data.NewMessage(channel, producer, messagePayload, contentType, data.HeadersMap{})
		dj := &data.DeliveryJob{Message: msg}
		pq.Enqueue(&Job{Data: dj, Priority: uint(priority)})
	}

	for i := 0; i < MaxQueueSize; i++ {
		_ = pq.Dequeue()
	}

}
