package dispatcher

import (
	"net/url"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/newscred/webhook-broker/config"
	"github.com/newscred/webhook-broker/storage/data"
)

func newLaneTestConsumer(t *testing.T, channelID, consumerID string) *data.Consumer {
	t.Helper()
	laneChannel, err := data.NewChannel(channelID, "token")
	require.NoError(t, err)
	laneChannel.QuickFix()
	callbackURL, _ := url.Parse("http://localhost/" + consumerID)
	consumer, err := data.NewConsumer(laneChannel, consumerID, "token", callbackURL, config.PushConsumerStr)
	require.NoError(t, err)
	consumer.QuickFix()
	return consumer
}

func newLaneTestJob(consumer *data.Consumer, priority uint) *Job {
	dj := &data.DeliveryJob{Listener: consumer, Priority: priority}
	dj.BasePaginateable.QuickFix()
	return NewJob(dj)
}

func fixedLimits(limits map[string]uint) func(channelID, consumerID string) uint {
	return func(channelID, consumerID string) uint {
		return limits[consumerID]
	}
}

func gaugeValue(t *testing.T, gauge prometheus.Gauge) float64 {
	t.Helper()
	metric := &dto.Metric{}
	require.NoError(t, gauge.Write(metric))
	return metric.GetGauge().GetValue()
}

func drainReady(s *laneScheduler) []*Job {
	taken := make([]*Job, 0)
	for job := s.tryNext(); job != nil; job = s.tryNext() {
		taken = append(taken, job)
	}
	return taken
}

func TestLaneScheduler_CapsInflightPerConsumer(t *testing.T) {
	slow := newLaneTestConsumer(t, "lane-cap-channel", "lane-cap-slow")
	s := newLaneScheduler(fixedLimits(map[string]uint{"lane-cap-slow": 2}), counter)
	for i := 0; i < 5; i++ {
		assert.True(t, s.enqueue(newLaneTestJob(slow, 0)))
	}

	running := drainReady(s)
	assert.Len(t, running, 2)

	s.done(running[0])
	next := drainReady(s)
	assert.Len(t, next, 1)
	assert.Nil(t, s.tryNext())
}

func TestLaneScheduler_SlowConsumerDoesNotBlockOthers(t *testing.T) {
	slow := newLaneTestConsumer(t, "lane-iso-channel", "lane-iso-slow")
	fast := newLaneTestConsumer(t, "lane-iso-channel", "lane-iso-fast")
	s := newLaneScheduler(fixedLimits(map[string]uint{"lane-iso-slow": 1}), counter)
	for i := 0; i < 10; i++ {
		s.enqueue(newLaneTestJob(slow, 0))
	}
	for i := 0; i < 3; i++ {
		s.enqueue(newLaneTestJob(fast, 0))
	}

	taken := drainReady(s)
	consumersTaken := make(map[string]int)
	for _, job := range taken {
		consumersTaken[job.Data.Listener.ConsumerID]++
	}
	assert.Equal(t, 1, consumersTaken["lane-iso-slow"])
	assert.Equal(t, 3, consumersTaken["lane-iso-fast"])
}

func TestLaneScheduler_RecreatedConsumerSharesLaneAndLimit(t *testing.T) {
	original := newLaneTestConsumer(t, "lane-recreate-channel", "lane-recreate")
	recreated := newLaneTestConsumer(t, "lane-recreate-channel", "lane-recreate")
	require.NotEqual(t, original.ID, recreated.ID)
	s := newLaneScheduler(fixedLimits(map[string]uint{"lane-recreate": 1}), counter)
	s.enqueue(newLaneTestJob(original, 0))
	s.enqueue(newLaneTestJob(recreated, 0))

	assert.Len(t, drainReady(s), 1)
	assert.Len(t, s.lanes, 1)
}

func TestLaneScheduler_RoundRobinAcrossConsumers(t *testing.T) {
	first := newLaneTestConsumer(t, "lane-rr-channel", "lane-rr-first")
	second := newLaneTestConsumer(t, "lane-rr-channel", "lane-rr-second")
	s := newLaneScheduler(fixedLimits(nil), counter)
	for i := 0; i < 4; i++ {
		s.enqueue(newLaneTestJob(first, 0))
	}
	for i := 0; i < 4; i++ {
		s.enqueue(newLaneTestJob(second, 0))
	}

	order := make([]string, 0)
	for _, job := range drainReady(s) {
		order = append(order, job.Data.Listener.ConsumerID)
	}
	assert.Equal(t, []string{
		"lane-rr-first", "lane-rr-second", "lane-rr-first", "lane-rr-second",
		"lane-rr-first", "lane-rr-second", "lane-rr-first", "lane-rr-second",
	}, order)
}

func TestLaneScheduler_DropsJobAlreadyQueuedOrRunning(t *testing.T) {
	consumer := newLaneTestConsumer(t, "lane-dedup-channel", "lane-dedup")
	s := newLaneScheduler(fixedLimits(nil), counter)
	job := newLaneTestJob(consumer, 0)
	duplicate := NewJob(job.Data)

	assert.True(t, s.enqueue(job))
	assert.False(t, s.enqueue(duplicate), "queued job must not be queued again")

	running := s.tryNext()
	require.NotNil(t, running)
	assert.False(t, s.enqueue(duplicate), "running job must not be queued again")
	assert.Nil(t, s.tryNext())

	s.done(running)
	assert.True(t, s.enqueue(duplicate), "job may be queued again once its attempt finished")
}

func TestLaneScheduler_PriorityThenFIFOWithinConsumer(t *testing.T) {
	consumer := newLaneTestConsumer(t, "lane-order-channel", "lane-order")
	s := newLaneScheduler(fixedLimits(nil), counter)
	lowFirst := newLaneTestJob(consumer, 1)
	lowSecond := newLaneTestJob(consumer, 1)
	lowThird := newLaneTestJob(consumer, 1)
	high := newLaneTestJob(consumer, 5)
	for _, job := range []*Job{lowFirst, lowSecond, high, lowThird} {
		s.enqueue(job)
	}

	assert.Equal(t, []*Job{high, lowFirst, lowSecond, lowThird}, drainReady(s))
}

func TestLaneScheduler_HighestPriorityFirstAcrossConsumers(t *testing.T) {
	busy := newLaneTestConsumer(t, "lane-xprio-channel", "lane-xprio-busy")
	other := newLaneTestConsumer(t, "lane-xprio-channel", "lane-xprio-other")
	urgent := newLaneTestConsumer(t, "lane-xprio-channel", "lane-xprio-urgent")
	s := newLaneScheduler(fixedLimits(nil), counter)
	for i := 0; i < 3; i++ {
		s.enqueue(newLaneTestJob(busy, 0))
	}
	s.enqueue(newLaneTestJob(other, 0))
	high := newLaneTestJob(urgent, 100)
	s.enqueue(high)

	assert.Equal(t, high, s.tryNext())
	order := make([]string, 0)
	for _, job := range drainReady(s) {
		order = append(order, job.Data.Listener.ConsumerID)
	}
	assert.Equal(t, []string{"lane-xprio-busy", "lane-xprio-other", "lane-xprio-busy", "lane-xprio-busy"}, order)
}

func TestLaneScheduler_FullHighPriorityLaneDoesNotBlockOthers(t *testing.T) {
	urgent := newLaneTestConsumer(t, "lane-xfull-channel", "lane-xfull-urgent")
	other := newLaneTestConsumer(t, "lane-xfull-channel", "lane-xfull-other")
	s := newLaneScheduler(fixedLimits(map[string]uint{"lane-xfull-urgent": 1}), counter)
	first := newLaneTestJob(urgent, 100)
	s.enqueue(first)
	s.enqueue(newLaneTestJob(urgent, 100))
	low := newLaneTestJob(other, 0)
	s.enqueue(low)

	assert.Equal(t, first, s.tryNext())
	assert.Equal(t, low, s.tryNext())
	assert.Nil(t, s.tryNext())
}

func TestLaneScheduler_RemovesIdleLane(t *testing.T) {
	consumer := newLaneTestConsumer(t, "lane-idle-channel", "lane-idle")
	s := newLaneScheduler(fixedLimits(nil), counter)
	s.enqueue(newLaneTestJob(consumer, 0))
	job := s.tryNext()
	require.NotNil(t, job)
	assert.Len(t, s.lanes, 1)

	s.done(job)
	assert.Empty(t, s.lanes)
	assert.Empty(t, s.tracked)
}

func TestLaneScheduler_NextWaitsForWorkAndStops(t *testing.T) {
	consumer := newLaneTestConsumer(t, "lane-wait-channel", "lane-wait")
	s := newLaneScheduler(fixedLimits(nil), counter)
	stop := make(chan struct{})
	got := make(chan *Job, 1)
	go func() { got <- s.next(stop) }()

	job := newLaneTestJob(consumer, 0)
	s.enqueue(job)
	select {
	case taken := <-got:
		assert.Equal(t, job, taken)
	case <-time.After(2 * time.Second):
		t.Fatal("next did not return the enqueued job")
	}

	go func() { got <- s.next(stop) }()
	close(stop)
	select {
	case taken := <-got:
		assert.Nil(t, taken)
	case <-time.After(2 * time.Second):
		t.Fatal("next did not return after stop")
	}
}

func TestLaneScheduler_PerConsumerMetrics(t *testing.T) {
	consumer := newLaneTestConsumer(t, "lane-metrics-channel", "lane-metrics")
	s := newLaneScheduler(fixedLimits(map[string]uint{"lane-metrics": 1}), counter)
	queued := counter.ConsumerQueuedJobCount.WithLabelValues("lane-metrics-channel", "lane-metrics")
	inflight := counter.ConsumerInflightJobCount.WithLabelValues("lane-metrics-channel", "lane-metrics")
	queuedBefore, inflightBefore := gaugeValue(t, queued), gaugeValue(t, inflight)
	s.enqueue(newLaneTestJob(consumer, 0))
	s.enqueue(newLaneTestJob(consumer, 0))
	assert.Equal(t, float64(2), gaugeValue(t, queued)-queuedBefore)

	job := s.tryNext()
	assert.Equal(t, float64(1), gaugeValue(t, queued)-queuedBefore)
	assert.Equal(t, float64(1), gaugeValue(t, inflight)-inflightBefore)

	s.done(job)
	assert.Equal(t, float64(0), gaugeValue(t, inflight)-inflightBefore)
	s.discardQueued()
	assert.Equal(t, float64(0), gaugeValue(t, queued)-queuedBefore)
}

func TestLaneScheduler_DiscardQueuedClearsQueuedJobsAndGauges(t *testing.T) {
	consumer := newLaneTestConsumer(t, "lane-discard-channel", "lane-discard")
	s := newLaneScheduler(fixedLimits(map[string]uint{"lane-discard": 1}), counter)
	queued := counter.ConsumerQueuedJobCount.WithLabelValues("lane-discard-channel", "lane-discard")
	inflight := counter.ConsumerInflightJobCount.WithLabelValues("lane-discard-channel", "lane-discard")
	queuedBefore, inflightBefore := gaugeValue(t, queued), gaugeValue(t, inflight)
	waiting := newLaneTestJob(consumer, 0)
	s.enqueue(newLaneTestJob(consumer, 0))
	s.enqueue(waiting)
	running := s.tryNext()
	require.NotNil(t, running)

	s.discardQueued()
	assert.Equal(t, float64(0), gaugeValue(t, queued)-queuedBefore)
	assert.Equal(t, float64(1), gaugeValue(t, inflight)-inflightBefore)
	assert.Nil(t, s.tryNext())
	assert.True(t, s.enqueue(waiting), "a discarded job is no longer held")
	s.discardQueued()

	s.done(running)
	assert.Equal(t, float64(0), gaugeValue(t, inflight)-inflightBefore)
	assert.Empty(t, s.lanes)
	assert.Empty(t, s.tracked)
}

// Generated with assistance from Claude AI

func TestLaneScheduler_IsHeld(t *testing.T) {
	consumer := newLaneTestConsumer(t, "lane-held-channel", "lane-held")
	s := newLaneScheduler(fixedLimits(nil), counter)
	job := newLaneTestJob(consumer, 0)
	id := job.Data.ID.String()
	assert.False(t, s.isHeld(id))
	require.True(t, s.enqueue(job))
	assert.True(t, s.isHeld(id), "a queued job is held")
	running := s.tryNext()
	require.NotNil(t, running)
	assert.True(t, s.isHeld(id), "a running job is held")
	s.done(running)
	assert.False(t, s.isHeld(id), "a finished job is no longer held")
	require.True(t, s.enqueue(NewJob(job.Data)))
	s.discardQueued()
	assert.False(t, s.isHeld(id), "a discarded job is no longer held")
}
