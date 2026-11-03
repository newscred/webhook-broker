package dispatcher

import (
	"sync"
)

// consumerLane holds the jobs waiting for one consumer and how many of its deliveries are running.
type consumerLane struct {
	key        string
	channelID  string
	consumerID string
	queue      *PriorityQueue
	inflight   uint
	limit      uint
	ready      bool
}

func (lane *consumerLane) hasRoom() bool {
	return lane.limit == 0 || lane.inflight < lane.limit
}

func (lane *consumerLane) canRun() bool {
	return lane.queue.Len() > 0 && lane.hasRoom()
}

// laneScheduler hands jobs to workers one consumer lane at a time, in round-robin order, and skips
// any lane that already runs its limit of deliveries. A slow consumer can therefore only hold up
// its own jobs, never another consumer's. It also drops a job already queued or running on this
// instance, which the recovery workers would otherwise enqueue again on every pass.
type laneScheduler struct {
	mu       sync.Mutex
	lanes    map[string]*consumerLane
	ready    []*consumerLane
	tracked  map[string]struct{}
	seq      uint64
	limitFor func(channelID, consumerID string) uint
	metrics  *MetricsContainer
	wake     chan struct{}
}

func newLaneScheduler(limitFor func(channelID, consumerID string) uint, metrics *MetricsContainer) *laneScheduler {
	return &laneScheduler{
		lanes:    make(map[string]*consumerLane),
		tracked:  make(map[string]struct{}),
		limitFor: limitFor,
		metrics:  metrics,
		wake:     make(chan struct{}, 1),
	}
}

func laneIdentity(job *Job) (channelID, consumerID, key string) {
	consumerID = job.Data.Listener.ConsumerID
	if job.Data.Listener.ConsumingFrom != nil {
		channelID = job.Data.Listener.ConsumingFrom.ChannelID
	} else if job.Data.Message != nil && job.Data.Message.BroadcastedTo != nil {
		channelID = job.Data.Message.BroadcastedTo.ChannelID
	}
	return channelID, consumerID, job.Data.Listener.ID.String()
}

// enqueue adds the job to its consumer's lane; it returns false and drops the job when the same
// job is already queued or running on this instance.
func (s *laneScheduler) enqueue(job *Job) bool {
	jobID := job.Data.ID.String()
	s.mu.Lock()
	if _, exists := s.tracked[jobID]; exists {
		s.mu.Unlock()
		return false
	}
	s.tracked[jobID] = struct{}{}
	channelID, consumerID, key := laneIdentity(job)
	lane, ok := s.lanes[key]
	if !ok {
		lane = &consumerLane{key: key, channelID: channelID, consumerID: consumerID, queue: NewJobPriorityQueue(s.metrics.QueuedJobCount), limit: s.limitFor(channelID, consumerID)}
		s.lanes[key] = lane
	}
	s.seq++
	job.seq = s.seq
	lane.queue.Enqueue(job)
	s.metrics.ConsumerQueuedJobCount.WithLabelValues(channelID, consumerID).Inc()
	s.markReadyLocked(lane)
	s.mu.Unlock()
	s.signal()
	return true
}

func (s *laneScheduler) markReadyLocked(lane *consumerLane) {
	if !lane.ready && lane.canRun() {
		lane.ready = true
		s.ready = append(s.ready, lane)
	}
}

func (s *laneScheduler) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// tryNext pops the next job from the lane at the head of the round-robin, or returns nil when no
// lane can run.
func (s *laneScheduler) tryNext() *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.ready) > 0 {
		lane := s.ready[0]
		s.ready[0] = nil
		s.ready = s.ready[1:]
		lane.ready = false
		if !lane.canRun() {
			continue
		}
		job := lane.queue.Dequeue()
		lane.inflight++
		s.metrics.ConsumerQueuedJobCount.WithLabelValues(lane.channelID, lane.consumerID).Dec()
		s.metrics.ConsumerInflightJobCount.WithLabelValues(lane.channelID, lane.consumerID).Inc()
		s.markReadyLocked(lane)
		return job
	}
	return nil
}

// next blocks until a job can run or stop is closed; it returns nil when stopped.
func (s *laneScheduler) next(stop <-chan struct{}) *Job {
	for {
		if job := s.tryNext(); job != nil {
			return job
		}
		select {
		case <-s.wake:
		case <-stop:
			return nil
		}
	}
}

// done releases the job's slot in its lane once the delivery attempt has finished.
func (s *laneScheduler) done(job *Job) {
	_, _, key := laneIdentity(job)
	s.mu.Lock()
	delete(s.tracked, job.Data.ID.String())
	lane, ok := s.lanes[key]
	if ok {
		if lane.inflight > 0 {
			lane.inflight--
			s.metrics.ConsumerInflightJobCount.WithLabelValues(lane.channelID, lane.consumerID).Dec()
		}
		if lane.queue.Len() == 0 && lane.inflight == 0 && !lane.ready {
			delete(s.lanes, key)
		} else {
			s.markReadyLocked(lane)
		}
	}
	s.mu.Unlock()
	s.signal()
}

// Generated with assistance from Claude AI
