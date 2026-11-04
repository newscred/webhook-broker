package parking

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/newscred/webhook-broker/dispatcher"
	dispatchermocks "github.com/newscred/webhook-broker/dispatcher/mocks"
	"github.com/newscred/webhook-broker/storage/data"
)

// handoffRecorder is an httptest server that records hand-off requests and plays scripted status codes
type handoffRecorder struct {
	server   *httptest.Server
	mutex    sync.Mutex
	requests []*http.Request
	bodies   []EnqueueRequest
	statuses []int
	delay    time.Duration
}

func newHandoffRecorder(statuses ...int) *handoffRecorder {
	recorder := &handoffRecorder{statuses: statuses}
	recorder.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		request := EnqueueRequest{}
		json.Unmarshal(body, &request)
		recorder.mutex.Lock()
		index := len(recorder.requests)
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		recorder.requests = append(recorder.requests, r)
		recorder.bodies = append(recorder.bodies, request)
		status := http.StatusAccepted
		if index < len(recorder.statuses) {
			status = recorder.statuses[index]
		} else if len(recorder.statuses) > 0 {
			status = recorder.statuses[len(recorder.statuses)-1]
		}
		delay := recorder.delay
		recorder.mutex.Unlock()
		time.Sleep(delay)
		w.WriteHeader(status)
	}))
	return recorder
}

func (recorder *handoffRecorder) count() int {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return len(recorder.requests)
}

func (recorder *handoffRecorder) receivedIDs() []string {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	ids := make([]string, 0)
	for _, body := range recorder.bodies {
		ids = append(ids, body.JobIDs...)
	}
	return ids
}

func noSleep(t *testing.T) func() {
	old := handoffSleep
	handoffSleep = func(int) {}
	return func() { handoffSleep = old }
}

func newTestSender(recorder *handoffRecorder, messageDispatcher dispatcher.MessageDispatcher, mutate func(*HandoffSenderConfiguration)) *HandoffSender {
	cfg := HandoffSenderConfiguration{Dispatcher: messageDispatcher, Metrics: dispatcher.NewMetricsContainer(), HTTPEnabled: true,
		Endpoint: recorder.server.URL + InternalEnqueuePath, AdminToken: "admin-token", Timeout: time.Second, MaxAttempts: 3, Concurrency: 1, LocalFallback: false}
	if mutate != nil {
		mutate(&cfg)
	}
	return NewHandoffSender(cfg)
}

func sendAndWait(sender *HandoffSender, jobs []*data.DeliveryJob) {
	sender.Start()
	sender.Submit(jobs)
	sender.Stop()
}

func TestHandoffSuccess(t *testing.T) {
	recorder := newHandoffRecorder(http.StatusAccepted)
	defer recorder.server.Close()
	sender := newTestSender(recorder, new(dispatchermocks.MessageDispatcher), nil)
	before := counterValue(t, sender.cfg.Metrics.HandoffRequestsTotal.WithLabelValues(outcomeAccepted))
	jobs := jobsOf(2)
	sendAndWait(sender, jobs)
	if assert.Equal(t, 1, recorder.count()) {
		request := recorder.requests[0]
		assert.Equal(t, InternalEnqueuePath, request.URL.Path)
		assert.Equal(t, "admin-token", request.Header.Get(AdminTokenHeader))
		assert.Equal(t, "application/json", request.Header.Get("Content-Type"))
		assert.Empty(t, request.Header.Get(SignatureHeader), "no signature without gateway auth")
		assert.Equal(t, []string{jobs[0].ID.String(), jobs[1].ID.String()}, recorder.receivedIDs())
	}
	assert.Equal(t, float64(1), counterValue(t, sender.cfg.Metrics.HandoffRequestsTotal.WithLabelValues(outcomeAccepted))-before)
}

func TestHandoffSignsWhenHMACEnabled(t *testing.T) {
	recorder := newHandoffRecorder(http.StatusAccepted)
	defer recorder.server.Close()
	secret := []byte("shared-secret")
	sender := newTestSender(recorder, new(dispatchermocks.MessageDispatcher), func(cfg *HandoffSenderConfiguration) { cfg.HMACSecret = secret })
	sendAndWait(sender, jobsOf(1))
	if assert.Equal(t, 1, recorder.count()) {
		request := recorder.requests[0]
		body, _ := io.ReadAll(request.Body)
		signature := request.Header.Get(SignatureHeader)
		parts := strings.Split(signature, ",")
		if assert.Equal(t, 2, len(parts)) {
			timestamp := strings.TrimPrefix(parts[0], "t=")
			_, err := strconv.ParseInt(timestamp, 10, 64)
			assert.NoError(t, err)
			mac := hmac.New(sha256.New, secret)
			mac.Write([]byte(timestamp + "." + string(body)))
			assert.Equal(t, "v1="+hex.EncodeToString(mac.Sum(nil)), parts[1])
		}
	}
}

func TestSignPayload(t *testing.T) {
	signature := SignPayload([]byte("secret"), 1700000000, []byte(`{"jobIds":["a"]}`))
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write([]byte(`1700000000.{"jobIds":["a"]}`))
	assert.Equal(t, "t=1700000000,v1="+hex.EncodeToString(mac.Sum(nil)), signature)
}

func TestHandoffRetriesOn503(t *testing.T) {
	defer noSleep(t)()
	recorder := newHandoffRecorder(http.StatusServiceUnavailable, http.StatusServiceUnavailable, http.StatusAccepted)
	defer recorder.server.Close()
	sender := newTestSender(recorder, new(dispatchermocks.MessageDispatcher), nil)
	busyBefore := counterValue(t, sender.cfg.Metrics.HandoffRequestsTotal.WithLabelValues(outcomeBusy))
	acceptedBefore := counterValue(t, sender.cfg.Metrics.HandoffRequestsTotal.WithLabelValues(outcomeAccepted))
	sendAndWait(sender, jobsOf(1))
	assert.Equal(t, 3, recorder.count())
	assert.Equal(t, float64(2), counterValue(t, sender.cfg.Metrics.HandoffRequestsTotal.WithLabelValues(outcomeBusy))-busyBefore)
	assert.Equal(t, float64(1), counterValue(t, sender.cfg.Metrics.HandoffRequestsTotal.WithLabelValues(outcomeAccepted))-acceptedBefore)
}

func TestHandoffTimeout(t *testing.T) {
	defer noSleep(t)()
	recorder := newHandoffRecorder(http.StatusAccepted)
	recorder.delay = 200 * time.Millisecond
	defer recorder.server.Close()
	sender := newTestSender(recorder, new(dispatchermocks.MessageDispatcher), func(cfg *HandoffSenderConfiguration) {
		cfg.Timeout = 30 * time.Millisecond
		cfg.MaxAttempts = 1
	})
	failuresBefore := counterValue(t, sender.cfg.Metrics.HandoffFailuresTotal)
	errorsBefore := counterValue(t, sender.cfg.Metrics.HandoffRequestsTotal.WithLabelValues(outcomeError))
	sendAndWait(sender, jobsOf(1))
	assert.Equal(t, float64(1), counterValue(t, sender.cfg.Metrics.HandoffFailuresTotal)-failuresBefore)
	assert.Equal(t, float64(1), counterValue(t, sender.cfg.Metrics.HandoffRequestsTotal.WithLabelValues(outcomeError))-errorsBefore)
}

func TestHandoffAllFail(t *testing.T) {
	defer noSleep(t)()
	t.Run("LocalFallback", func(t *testing.T) {
		recorder := newHandoffRecorder(http.StatusInternalServerError)
		defer recorder.server.Close()
		jobs := jobsOf(2)
		messageDispatcher := new(dispatchermocks.MessageDispatcher)
		messageDispatcher.On("EnqueueJobs", jobs).Return(2).Once()
		sender := newTestSender(recorder, messageDispatcher, func(cfg *HandoffSenderConfiguration) {
			cfg.MaxAttempts = 2
			cfg.LocalFallback = true
		})
		localBefore := counterValue(t, sender.cfg.Metrics.HandoffRequestsTotal.WithLabelValues(outcomeLocal))
		sendAndWait(sender, jobs)
		assert.Equal(t, 2, recorder.count())
		messageDispatcher.AssertExpectations(t)
		assert.Equal(t, float64(1), counterValue(t, sender.cfg.Metrics.HandoffRequestsTotal.WithLabelValues(outcomeLocal))-localBefore)
	})
	t.Run("NoFallback", func(t *testing.T) {
		recorder := newHandoffRecorder(http.StatusInternalServerError)
		defer recorder.server.Close()
		messageDispatcher := new(dispatchermocks.MessageDispatcher)
		sender := newTestSender(recorder, messageDispatcher, func(cfg *HandoffSenderConfiguration) { cfg.MaxAttempts = 2 })
		sendAndWait(sender, jobsOf(1))
		assert.Equal(t, 2, recorder.count())
		messageDispatcher.AssertNotCalled(t, "EnqueueJobs", mock.Anything)
	})
	t.Run("UnreachableEndpoint", func(t *testing.T) {
		messageDispatcher := new(dispatchermocks.MessageDispatcher)
		messageDispatcher.On("EnqueueJobs", mock.Anything).Return(1).Once()
		sender := NewHandoffSender(HandoffSenderConfiguration{Dispatcher: messageDispatcher, Metrics: dispatcher.NewMetricsContainer(), HTTPEnabled: true,
			Endpoint: "http://127.0.0.1:1" + InternalEnqueuePath, AdminToken: "t", Timeout: 100 * time.Millisecond, MaxAttempts: 1, Concurrency: 1, LocalFallback: true})
		sendAndWait(sender, jobsOf(1))
		messageDispatcher.AssertExpectations(t)
	})
}

func TestHandoffLocalMode(t *testing.T) {
	recorder := newHandoffRecorder(http.StatusAccepted)
	defer recorder.server.Close()
	jobs := jobsOf(1)
	messageDispatcher := new(dispatchermocks.MessageDispatcher)
	messageDispatcher.On("EnqueueJobs", jobs).Return(1).Once()
	sender := newTestSender(recorder, messageDispatcher, func(cfg *HandoffSenderConfiguration) { cfg.HTTPEnabled = false })
	sendAndWait(sender, jobs)
	assert.Equal(t, 0, recorder.count())
	messageDispatcher.AssertExpectations(t)
}

func TestHandoffSubmitSemantics(t *testing.T) {
	sender := NewHandoffSender(HandoffSenderConfiguration{Dispatcher: new(dispatchermocks.MessageDispatcher), Metrics: dispatcher.NewMetricsContainer(), Concurrency: 0, MaxAttempts: 0})
	assert.Equal(t, 1, sender.cfg.Concurrency, "concurrency is at least 1")
	assert.Equal(t, 1, sender.cfg.MaxAttempts, "attempts are at least 1")
	assert.True(t, sender.Submit(nil), "empty batches are accepted without queueing")
	assert.True(t, sender.Submit(jobsOf(1)))
	assert.True(t, sender.Submit(jobsOf(1)))
	assert.True(t, sender.Saturated())
	assert.False(t, sender.Submit(jobsOf(1)), "a saturated sender rejects without blocking")
	sender.Stop()
	assert.False(t, sender.Submit(jobsOf(1)), "a stopped sender rejects")
}

func TestHandoffConcurrentSubmit_Race(t *testing.T) {
	var accepted int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&accepted, 1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	sender := NewHandoffSender(HandoffSenderConfiguration{Dispatcher: new(dispatchermocks.MessageDispatcher), Metrics: dispatcher.NewMetricsContainer(),
		HTTPEnabled: true, Endpoint: server.URL + InternalEnqueuePath, AdminToken: "t", Timeout: time.Second, MaxAttempts: 1, Concurrency: 4})
	sender.Start()
	var wg sync.WaitGroup
	var submitted int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if sender.Submit(jobsOf(1)) {
					atomic.AddInt32(&submitted, 1)
				} else {
					time.Sleep(time.Millisecond)
				}
			}
		}()
	}
	wg.Wait()
	sender.Stop()
	assert.Equal(t, atomic.LoadInt32(&submitted), atomic.LoadInt32(&accepted))
}

func TestBuildEnqueueEndpoint(t *testing.T) {
	assert.Equal(t, "http://lb:8080"+InternalEnqueuePath, BuildEnqueueEndpoint("http://lb:8080"))
	assert.Equal(t, "http://lb:8080"+InternalEnqueuePath, BuildEnqueueEndpoint("http://lb:8080/"))
}

// Generated with assistance from Claude AI
