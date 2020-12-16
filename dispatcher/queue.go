package dispatcher

import (
	"container/list"
	"sync"
)

// A PriorityQueue implements heap.Interface and holds Items.
type PriorityQueue struct {
	jobs *list.List
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
	var marker *list.Element
	var firstElement bool = true
	for e := pq.jobs.Back(); e != nil; e = e.Prev() {
		firstElement = false
		queuedJob := e.Value.(*Job)
		if job.Priority <= queuedJob.Priority {
			marker = e
			break
		}
	}
	if firstElement {
		pq.jobs.PushFront(job)
	} else if marker == nil {
		pq.jobs.PushFront(job)
	} else {
		pq.jobs.InsertAfter(job, marker)
	}
}

// Dequeue pops the item next in order
func (pq *PriorityQueue) Dequeue() *Job {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	frontElement := pq.jobs.Front()
	job := pq.jobs.Remove(frontElement).(*Job)
	return job
}

// NewJobPriorityQueue initializes a priority queue for Jobs
func NewJobPriorityQueue() *PriorityQueue {
	return &PriorityQueue{jobs: list.New()}
}
