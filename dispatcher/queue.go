package dispatcher

import (
	"container/heap"
	"sync"
	"github.com/prometheus/client_golang/prometheus"
)

type jobs []*Job

func (jbs jobs) Len() int {
	return len(jbs)
}

func (jbs jobs) Swap(i, j int) {
	jbs[i], jbs[j] = jbs[j], jbs[i]
}

func (jbs jobs) Less(i, j int) bool {
	// We want Pop to give us the highest, not lowest, priority so we use greater than here.
	return jbs[i].Priority > jbs[j].Priority
}

func (jbs *jobs) Push(x any) {
	item := x.(*Job)
	*jbs = append(*jbs, item)
}

func (jbs *jobs) Pop() any {
	old := *jbs
	n := len(old)
	item := old[n-1]
	old[n-1] = nil // avoid memory leak
	*jbs = old[0 : n-1]
	return item
}

type PriorityQueue struct {
	jobs jobs
	queuedCounter prometheus.Gauge
	mu   sync.Mutex
}

// Len returns the length of the priority queue
func (pq *PriorityQueue) Len() int {
	return pq.jobs.Len()
}

//Enqueue queues the item in its correct position
func (pq *PriorityQueue) Enqueue(job *Job) {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	heap.Push(&pq.jobs, job)
	pq.queuedCounter.Inc()
}

// Dequeue pops the item next in order
func (pq *PriorityQueue) Dequeue() *Job {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	pq.queuedCounter.Dec()
	return heap.Pop(&pq.jobs).(*Job)
}

// NewJobPriorityQueue initializes a priority queue for Jobs
func NewJobPriorityQueue(queuedCounter prometheus.Gauge) *PriorityQueue {
	return &PriorityQueue{queuedCounter: queuedCounter}
}
