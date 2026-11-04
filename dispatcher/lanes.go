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

// laneScheduler hands jobs to workers one consumer lane at a time, highest priority first and in
// round-robin order for equal priority, and skips any lane that already runs its limit of deliveries. A slow consumer can therefore only hold up
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
	// Keyed like the limit lookup, so a consumer deleted and created again while its old jobs still
	// run shares one lane, and one limit, with the new record.
	return channelID, consumerID, channelID + "/" + consumerID
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

// isHeld reports whether the job is queued or running on this instance.
func (s *laneScheduler) isHeld(jobID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, held := s.tracked[jobID]
	return held
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

// tryNext pops the next job from the ready lane whose next job has the highest priority, or returns
// nil when no lane can run. Lanes whose next jobs have equal priority are served in round-robin
// order, because a lane that was just served goes to the end of the ready list.
func (s *laneScheduler) tryNext() *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	best := -1
	var bestPriority uint
	runnable := s.ready[:0]
	for _, lane := range s.ready {
		if !lane.canRun() {
			lane.ready = false
			continue
		}
		runnable = append(runnable, lane)
		if priority := lane.queue.Peek().Priority; best < 0 || priority > bestPriority {
			best, bestPriority = len(runnable)-1, priority
		}
	}
	for i := len(runnable); i < len(s.ready); i++ {
		s.ready[i] = nil
	}
	s.ready = runnable
	if best < 0 {
		return nil
	}
	lane := s.ready[best]
	copy(s.ready[best:], s.ready[best+1:])
	s.ready[len(s.ready)-1] = nil
	s.ready = s.ready[:len(s.ready)-1]
	lane.ready = false
	job := lane.queue.Dequeue()
	lane.inflight++
	s.metrics.ConsumerQueuedJobCount.WithLabelValues(lane.channelID, lane.consumerID).Dec()
	s.metrics.ConsumerInflightJobCount.WithLabelValues(lane.channelID, lane.consumerID).Inc()
	s.markReadyLocked(lane)
	return job
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

// discardQueued drops every queued job when the dispatcher stops, so that the per-consumer gauges,
// which outlive the dispatcher, do not keep counting them. The jobs stay queued in the database for
// the next dispatcher. Running jobs still release their slot through done.
func (s *laneScheduler) discardQueued() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, lane := range s.lanes {
		for lane.queue.Len() > 0 {
			job := lane.queue.Dequeue()
			delete(s.tracked, job.Data.ID.String())
			s.metrics.ConsumerQueuedJobCount.WithLabelValues(lane.channelID, lane.consumerID).Dec()
		}
		lane.ready = false
		if lane.inflight == 0 {
			delete(s.lanes, key)
		}
	}
	s.ready = nil
}

// Generated with assistance from Claude AI
