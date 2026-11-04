package main

// Integration scenarios for dispatch pause with job parking (tech spec docs/tech-specs/dispatch-pause-spec.md §18.10).
// Exit codes 110-149 are reserved for these scenarios.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	adminTokenHeader      = "X-Broker-Admin-Token"
	itestAdminToken       = "integration-test-admin-token"
	dispatchPausedHeader  = "X-Broker-Dispatch-Paused"
	brokerMessageIDHeader = "X-Broker-Message-ID"
	defaultBroker2BaseURL = "http://webhook-broker-2:8083"
	defaultMySQLURL       = "webhook_broker:zxc909zxc@tcp(mysql:3306)/webhook-broker?charset=utf8mb4&collation=utf8mb4_0900_ai_ci&parseTime=true&multiStatements=true"
)

var (
	consumerPortString string
	broker2BaseURL     string
)

func exitIf(failed bool, code int, format string, args ...interface{}) {
	if failed {
		log.Printf("dispatch pause test failed (exit %d): "+format, append([]interface{}{code}, args...)...)
		os.Exit(code)
	}
}

func waitFor(timeout time.Duration, condition func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return condition()
}

func pauseTestDB() *sql.DB {
	db, err := connectToMySQL(getEnvOrDefault("MYSQL_CONNECTION_URL", defaultMySQLURL))
	exitIf(err != nil, 110, "could not connect to MySQL: %v", err)
	return db
}

// deliveryRecorder is a consumer handler recording delivered message ids, delivery start times and delivering hosts
type deliveryRecorder struct {
	mutex    sync.Mutex
	received map[string]int
	starts   []time.Time
	hosts    map[string]int
	delay    time.Duration
}

func newDeliveryRecorder() *deliveryRecorder {
	return &deliveryRecorder{received: map[string]int{}, hosts: map[string]int{}}
}

func (recorder *deliveryRecorder) handler() func(string, http.ResponseWriter, *http.Request) {
	return func(_ string, w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		io.ReadAll(r.Body)
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		recorder.mutex.Lock()
		recorder.starts = append(recorder.starts, start)
		recorder.received[r.Header.Get(brokerMessageIDHeader)]++
		recorder.hosts[host]++
		delay := recorder.delay
		recorder.mutex.Unlock()
		time.Sleep(delay)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (recorder *deliveryRecorder) unique() int {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return len(recorder.received)
}

func (recorder *deliveryRecorder) total() int {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return len(recorder.starts)
}

func (recorder *deliveryRecorder) duplicates() int {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	duplicates := 0
	for _, count := range recorder.received {
		duplicates += count - 1
	}
	return duplicates
}

func (recorder *deliveryRecorder) startsBetween(from, to time.Time) int {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	count := 0
	for _, start := range recorder.starts {
		if start.After(from) && start.Before(to) {
			count++
		}
	}
	return count
}

func (recorder *deliveryRecorder) distinctHosts() int {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return len(recorder.hosts)
}

func doRequest(method, requestURL string, body string, headers map[string]string) (int, http.Header, []byte) {
	req, _ := http.NewRequest(method, requestURL, strings.NewReader(body))
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Println("request error", method, requestURL, err)
		return 0, nil, nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, respBody
}

func pauseTestChannel(channelID string) {
	formValues := url.Values{}
	formValues.Add(tokenFormParamKey, token)
	status, _, _ := doRequest(http.MethodPut, brokerBaseURL+"/channel/"+channelID, formValues.Encode(), map[string]string{headerContentType: formDataContentTypeHeaderValue})
	exitIf(status != http.StatusOK && status != http.StatusBadRequest, 111, "could not create channel %s: %d", channelID, status)
}

func pauseTestConsumer(channelID, consumerID, consumerType string) {
	formValues := url.Values{}
	formValues.Add(tokenFormParamKey, token)
	formValues.Add(callbackURLFormParamKey, "http://"+consumerHostName+consumerPortString+"/"+consumerID)
	if consumerType != "" {
		formValues.Add(consumerTypeFormParamKey, consumerType)
	}
	status, _, body := doRequest(http.MethodPut, brokerBaseURL+"/channel/"+channelID+"/consumer/"+consumerID, formValues.Encode(),
		map[string]string{headerContentType: formDataContentTypeHeaderValue})
	exitIf(status != http.StatusOK && status != http.StatusBadRequest, 111, "could not create consumer %s: %d %s", consumerID, status, string(body))
}

func consumerPauseURL(channelID, consumerID string) string {
	return brokerBaseURL + "/channel/" + channelID + "/consumer/" + consumerID + "/dispatch-pause"
}

func consumerAuthHeaders() map[string]string {
	return map[string]string{headerChannelToken: token, headerConsumerToken: token, headerContentType: formDataContentTypeHeaderValue}
}

func pauseConsumerAPI(channelID, consumerID string) {
	status, _, body := doRequest(http.MethodPut, consumerPauseURL(channelID, consumerID), "reason=integration-test", consumerAuthHeaders())
	exitIf(status != http.StatusOK, 112, "pause %s returned %d %s", consumerID, status, string(body))
}

func resumeConsumerAPI(channelID, consumerID string) {
	status, _, _ := doRequest(http.MethodDelete, consumerPauseURL(channelID, consumerID), "", consumerAuthHeaders())
	exitIf(status != http.StatusNoContent && status != http.StatusNotFound, 112, "resume %s returned %d", consumerID, status)
}

func globalPauseAPI(pause bool) {
	method, expected := http.MethodPut, http.StatusOK
	if !pause {
		method, expected = http.MethodDelete, http.StatusNoContent
	}
	status, _, body := doRequest(method, brokerBaseURL+"/dispatch-pause", "reason=integration-test",
		map[string]string{adminTokenHeader: itestAdminToken, headerContentType: formDataContentTypeHeaderValue})
	exitIf(status != expected && !(method == http.MethodDelete && status == http.StatusNotFound), 113, "global pause=%v returned %d %s", pause, status, string(body))
}

type dispatchPausesResponse struct {
	GloballyPaused bool `json:"globallyPaused"`
	Pauses         []struct {
		Scope          string `json:"scope"`
		ConsumerID     string `json:"consumerId"`
		ParkedJobCount int64  `json:"parkedJobCount"`
	} `json:"pauses"`
	Parked []struct {
		ChannelID  string `json:"channelId"`
		ConsumerID string `json:"consumerId"`
		Count      int64  `json:"count"`
		Paused     bool   `json:"paused"`
	} `json:"parked"`
}

func getDispatchPauses() dispatchPausesResponse {
	status, _, body := doRequest(http.MethodGet, brokerBaseURL+"/dispatch-pauses", "", map[string]string{adminTokenHeader: itestAdminToken})
	exitIf(status != http.StatusOK, 114, "dispatch-pauses returned %d %s", status, string(body))
	result := dispatchPausesResponse{}
	exitIf(json.Unmarshal(body, &result) != nil, 114, "could not decode dispatch-pauses: %s", string(body))
	return result
}

func parkedCount(db *sql.DB, channelID, consumerID string) int {
	count := 0
	err := db.QueryRow("SELECT COUNT(*) FROM job_parking p JOIN consumer c ON p.consumerId = c.id WHERE c.channelId = ? AND c.consumerId = ?", channelID, consumerID).Scan(&count)
	exitIf(err != nil, 115, "could not count parked jobs: %v", err)
	return count
}

func assertNoJobOverlap(db *sql.DB, code int) {
	overlap := 0
	err := db.QueryRow("SELECT COUNT(*) FROM job j JOIN job_parking p ON j.id = p.id OR (j.messageId = p.messageId AND j.consumerId = p.consumerId)").Scan(&overlap)
	exitIf(err != nil || overlap != 0, code, "jobs present in both job and job_parking: %d (%v)", overlap, err)
}

// scrapeMetric sums the values of a Prometheus metric whose label set contains labelFilter; ok is false when the
// node can not be reached
func scrapeMetric(baseURL, name, labelFilter string) (float64, bool) {
	status, _, body := doRequest(http.MethodGet, baseURL+"/metrics", "", nil)
	if status != http.StatusOK {
		return 0, false
	}
	total := 0.0
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, name) || strings.HasPrefix(line, "#") || !strings.Contains(line, labelFilter) {
			continue
		}
		fields := strings.Fields(line)
		if value, err := strconv.ParseFloat(fields[len(fields)-1], 64); err == nil {
			total += value
		}
	}
	return total, true
}

func isReachable(baseURL string) bool {
	status, _, _ := doRequest(http.MethodGet, baseURL+"/_status", "", nil)
	return status == http.StatusOK
}

func testDispatchPause() {
	testDispatchPauseConsumer()
	resetHandlers()
	testDispatchPauseInflight()
	resetHandlers()
	testDispatchPauseGlobal()
	resetHandlers()
	testDispatchPausePull()
	resetHandlers()
	testDispatchPausePruneSafety()
	resetHandlers()
	testDispatchPauseHandoffDistribution()
	resetHandlers()
	testDispatchPauseChaos()
	resetHandlers()
	testDispatchPauseQueryPlans()
}

func testDispatchPauseConsumer() {
	log.Println("Starting dispatch pause: consumer pause")
	db := pauseTestDB()
	defer db.Close()
	channelID, paused, active := "itest-pause-consumer", "itest-pause-consumer-a", "itest-pause-consumer-b"
	pauseTestChannel(channelID)
	pauseTestConsumer(channelID, paused, "")
	pauseTestConsumer(channelID, active, "")
	pausedRecorder, activeRecorder := newDeliveryRecorder(), newDeliveryRecorder()
	consumerHandler[paused] = pausedRecorder.handler()
	consumerHandler[active] = activeRecorder.handler()
	pauseConsumerAPI(channelID, paused)
	exitIf(broadcastMessage(channelID, 200) != nil, 116, "broadcast failed")
	exitIf(!waitFor(30*time.Second, func() bool { return activeRecorder.unique() >= 200 }), 116, "active consumer received %d/200", activeRecorder.unique())
	time.Sleep(3 * time.Second)
	exitIf(pausedRecorder.total() != 0, 117, "paused consumer received %d deliveries", pausedRecorder.total())
	exitIf(parkedCount(db, channelID, paused) != 200, 117, "expected 200 parked jobs, found %d", parkedCount(db, channelID, paused))
	maxRetry := 0
	db.QueryRow("SELECT COALESCE(MAX(p.retryAttemptCount), 0) FROM job_parking p JOIN consumer c ON p.consumerId = c.id WHERE c.consumerId = ?", paused).Scan(&maxRetry)
	exitIf(maxRetry != 0, 117, "parking must not use retries, max retryAttemptCount %d", maxRetry)
	var messageID string
	db.QueryRow("SELECT m.messageId FROM job_parking p JOIN consumer c ON p.consumerId = c.id JOIN message m ON p.messageId = m.id WHERE c.consumerId = ? LIMIT 1", paused).Scan(&messageID)
	status, _, body := doRequest(http.MethodGet, brokerBaseURL+"/channel/"+channelID+"/message/"+messageID, "", nil)
	exitIf(status != http.StatusOK || !strings.Contains(string(body), "PARKED"), 117, "message %s does not show a PARKED job: %d %s", messageID, status, string(body))
	pauses := getDispatchPauses()
	parkedListed := false
	for _, parked := range pauses.Parked {
		if parked.ChannelID == channelID && parked.ConsumerID == paused {
			parkedListed = parked.Count == 200 && parked.Paused
		}
	}
	exitIf(!parkedListed, 117, "dispatch-pauses does not list 200 parked jobs for %s: %+v", paused, pauses.Parked)
	resumeConsumerAPI(channelID, paused)
	exitIf(!waitFor(60*time.Second, func() bool { return pausedRecorder.unique() >= 200 }), 118, "resumed consumer received %d/200", pausedRecorder.unique())
	exitIf(!waitFor(10*time.Second, func() bool { return parkedCount(db, channelID, paused) == 0 }), 118, "parking not empty after drain")
	assertNoJobOverlap(db, 118)
	log.Printf("Dispatch pause consumer test passed (duplicate deliveries: %d)\n", pausedRecorder.duplicates())
}

func testDispatchPauseInflight() {
	log.Println("Starting dispatch pause: pause with deliveries in flight")
	db := pauseTestDB()
	defer db.Close()
	channelID, consumerID := "itest-pause-inflight", "itest-pause-inflight-a"
	pauseTestChannel(channelID)
	pauseTestConsumer(channelID, consumerID, "")
	recorder := newDeliveryRecorder()
	recorder.delay = 2 * time.Second
	consumerHandler[consumerID] = recorder.handler()
	exitIf(broadcastMessage(channelID, 5) != nil, 119, "broadcast failed")
	exitIf(!waitFor(10*time.Second, func() bool { return recorder.total() >= 5 }), 119, "in-flight deliveries did not start: %d", recorder.total())
	pauseConsumerAPI(channelID, consumerID)
	pausedAt := time.Now()
	exitIf(broadcastMessage(channelID, 5) != nil, 119, "broadcast failed")
	time.Sleep(5 * time.Second)
	exitIf(recorder.startsBetween(pausedAt.Add(time.Second), time.Now()) != 0, 120, "deliveries started while paused")
	exitIf(recorder.unique() != 5, 120, "expected only the 5 in-flight messages delivered, got %d", recorder.unique())
	exitIf(parkedCount(db, channelID, consumerID) != 5, 120, "expected 5 parked jobs, found %d", parkedCount(db, channelID, consumerID))
	recorder.delay = 0
	resumeConsumerAPI(channelID, consumerID)
	exitIf(!waitFor(60*time.Second, func() bool { return recorder.unique() >= 10 }), 121, "after resume received %d/10", recorder.unique())
	log.Println("Dispatch pause in-flight test passed")
}

func testDispatchPauseGlobal() {
	log.Println("Starting dispatch pause: global pause")
	db := pauseTestDB()
	defer db.Close()
	channels := []string{"itest-pause-global-1", "itest-pause-global-2"}
	recorders := []*deliveryRecorder{newDeliveryRecorder(), newDeliveryRecorder()}
	for index, channelID := range channels {
		pauseTestChannel(channelID)
		pauseTestConsumer(channelID, channelID+"-a", "")
		consumerHandler[channelID+"-a"] = recorders[index].handler()
	}
	globalPauseAPI(true)
	exitIf(!getDispatchPauses().GloballyPaused, 122, "dispatch-pauses does not report the global pause")
	for _, channelID := range channels {
		exitIf(broadcastMessage(channelID, 20) != nil, 122, "broadcast failed")
	}
	time.Sleep(3 * time.Second)
	for index := range channels {
		exitIf(recorders[index].total() != 0, 123, "consumer on %s received %d deliveries during a global pause", channels[index], recorders[index].total())
	}
	globalPauseAPI(false)
	for index, channelID := range channels {
		recorder := recorders[index]
		exitIf(!waitFor(60*time.Second, func() bool { return recorder.unique() >= 20 }), 124, "after global resume %s received %d/20", channelID, recorder.unique())
		exitIf(!waitFor(10*time.Second, func() bool { return parkedCount(db, channelID, channelID+"-a") == 0 }), 125, "parking not empty for %s", channelID)
	}
	log.Println("Dispatch pause global test passed")
}

func fetchQueuedJobs(channelID, consumerID string) (JobListResult, http.Header) {
	status, header, body := doRequest(http.MethodGet, brokerBaseURL+"/channel/"+channelID+"/consumer/"+consumerID+"/queued-jobs?limit=100", "",
		map[string]string{headerChannelToken: token, headerConsumerToken: token})
	exitIf(status != http.StatusOK, 126, "queued-jobs returned %d %s", status, string(body))
	result := JobListResult{}
	json.Unmarshal(body, &result)
	return result, header
}

func transitionJob(channelID, consumerID, jobID, state string) int {
	body, _ := json.Marshal(JobStateUpdateModel{NextState: state})
	status, _, _ := doRequest(http.MethodPost, brokerBaseURL+"/channel/"+channelID+"/consumer/"+consumerID+"/job/"+jobID, string(body),
		map[string]string{headerChannelToken: token, headerConsumerToken: token, headerContentType: "application/json"})
	return status
}

func testDispatchPausePull() {
	log.Println("Starting dispatch pause: pull consumer")
	db := pauseTestDB()
	defer db.Close()
	channelID, consumerID := "itest-pause-pull", "itest-pause-pull-p"
	pauseTestChannel(channelID)
	pauseTestConsumer(channelID, consumerID, "pull")
	exitIf(broadcastMessage(channelID, 1) != nil, 126, "broadcast failed")
	var existing JobListResult
	exitIf(!waitFor(10*time.Second, func() bool { existing, _ = fetchQueuedJobs(channelID, consumerID); return len(existing.Result) == 1 }), 126, "pre-existing job not queued")
	pauseConsumerAPI(channelID, consumerID)
	paused, header := fetchQueuedJobs(channelID, consumerID)
	exitIf(len(paused.Result) != 0 || header.Get(dispatchPausedHeader) != "true", 127, "paused pull consumer got %d jobs, header %q", len(paused.Result), header.Get(dispatchPausedHeader))
	status := transitionJob(channelID, consumerID, existing.Result[0].ID.String(), JobInflightState)
	exitIf(status != http.StatusConflict, 128, "INFLIGHT while paused returned %d, expected 409", status)
	exitIf(broadcastMessage(channelID, 3) != nil, 128, "broadcast failed")
	exitIf(!waitFor(10*time.Second, func() bool { return parkedCount(db, channelID, consumerID) == 3 }), 129, "expected 3 parked pull jobs, found %d", parkedCount(db, channelID, consumerID))
	resumeConsumerAPI(channelID, consumerID)
	var resumed JobListResult
	exitIf(!waitFor(30*time.Second, func() bool { resumed, _ = fetchQueuedJobs(channelID, consumerID); return len(resumed.Result) == 4 }), 130,
		"after resume %d/4 pull jobs fetchable", len(resumed.Result))
	jobID := resumed.Result[0].ID.String()
	exitIf(transitionJob(channelID, consumerID, jobID, JobInflightState) != http.StatusAccepted, 131, "INFLIGHT after resume failed")
	exitIf(transitionJob(channelID, consumerID, jobID, JobDeliveredState) != http.StatusAccepted, 131, "DELIVERED after resume failed")
	log.Println("Dispatch pause pull test passed")
}

func testDispatchPausePruneSafety() {
	log.Println("Starting dispatch pause: prune safety")
	db := pauseTestDB()
	defer db.Close()
	channelID, paused, active := "itest-pause-prune", "itest-pause-prune-a", "itest-pause-prune-b"
	pauseTestChannel(channelID)
	pauseTestConsumer(channelID, paused, "")
	pauseTestConsumer(channelID, active, "")
	pausedRecorder, activeRecorder := newDeliveryRecorder(), newDeliveryRecorder()
	consumerHandler[paused] = pausedRecorder.handler()
	consumerHandler[active] = activeRecorder.handler()
	pauseConsumerAPI(channelID, paused)
	exitIf(broadcastMessage(channelID, 5) != nil, 132, "broadcast failed")
	exitIf(!waitFor(30*time.Second, func() bool { return activeRecorder.unique() >= 5 }), 132, "active consumer received %d/5", activeRecorder.unique())
	messageCount := func() int {
		count := 0
		db.QueryRow("SELECT COUNT(*) FROM message WHERE channelId = ?", channelID).Scan(&count)
		return count
	}
	_, err := db.Exec("UPDATE message SET createdAt = DATE_SUB(createdAt, INTERVAL 2 DAY), receivedAt = DATE_SUB(receivedAt, INTERVAL 2 DAY) WHERE channelId = ?", channelID)
	exitIf(err != nil, 133, "could not age messages: %v", err)
	time.Sleep(15 * time.Second) // the pruner runs continuously
	exitIf(messageCount() != 5, 134, "messages owed to a paused consumer were pruned: %d/5 left", messageCount())
	resumeConsumerAPI(channelID, paused)
	exitIf(!waitFor(60*time.Second, func() bool { return pausedRecorder.unique() >= 5 }), 135, "resumed consumer received %d/5", pausedRecorder.unique())
	exitIf(!waitFor(60*time.Second, func() bool { return messageCount() == 0 }), 135, "delivered messages were not pruned: %d left", messageCount())
	log.Println("Dispatch pause prune safety test passed")
}

func testDispatchPauseHandoffDistribution() {
	log.Println("Starting dispatch pause: hand-off distribution")
	db := pauseTestDB()
	defer db.Close()
	channelID, consumerID := "itest-pause-handoff", "itest-pause-handoff-a"
	pauseTestChannel(channelID)
	pauseTestConsumer(channelID, consumerID, "")
	recorder := newDeliveryRecorder()
	consumerHandler[consumerID] = recorder.handler()
	type node struct {
		name, baseURL string
		loadBalanced  bool
		before        float64
	}
	nodes := []*node{{name: "ibroker", baseURL: brokerBaseURL, loadBalanced: true}}
	secondNode := isReachable(broker2BaseURL)
	if secondNode {
		nodes = append(nodes, &node{name: "ibroker2", baseURL: broker2BaseURL, loadBalanced: true})
	}
	if isReachable(hmacBrokerBaseURL) {
		nodes = append(nodes, &node{name: "ibroker-hmac", baseURL: hmacBrokerBaseURL})
	}
	drainedFilter := `consumer="` + consumerID + `"`
	for _, n := range nodes {
		n.before, _ = scrapeMetric(n.baseURL, "drained_jobs_total", drainedFilter)
	}
	pauseConsumerAPI(channelID, consumerID)
	exitIf(broadcastMessage(channelID, 1000) != nil, 136, "broadcast failed")
	exitIf(!waitFor(30*time.Second, func() bool { return parkedCount(db, channelID, consumerID) == 1000 }), 136, "expected 1000 parked jobs, found %d", parkedCount(db, channelID, consumerID))
	resumeConsumerAPI(channelID, consumerID)
	exitIf(!waitFor(120*time.Second, func() bool { return recorder.unique() >= 1000 }), 137, "after resume received %d/1000", recorder.unique())
	totalDrained := 0.0
	loadBalancedDrainer := false
	for _, n := range nodes {
		after, ok := scrapeMetric(n.baseURL, "drained_jobs_total", drainedFilter)
		exitIf(!ok, 138, "could not scrape %s", n.name)
		delta := after - n.before
		totalDrained += delta
		if delta > 0 && n.loadBalanced {
			loadBalancedDrainer = true
		}
		log.Printf("node %s drained %.0f jobs\n", n.name, delta)
	}
	exitIf(totalDrained != 1000, 139, "drained %.0f jobs across nodes, expected 1000", totalDrained)
	log.Printf("deliveries came from %d distinct broker hosts (second node reachable: %v)\n", recorder.distinctHosts(), secondNode)
	if secondNode && loadBalancedDrainer {
		exitIf(recorder.distinctHosts() < 2, 140, "hand-off was not distributed across load-balanced nodes")
	}
	log.Println("Dispatch pause hand-off distribution test passed")
}

type pauseWindow struct {
	from, to time.Time
}

func testDispatchPauseChaos() {
	log.Println("Starting dispatch pause: pause/resume toggling under load")
	db := pauseTestDB()
	defer db.Close()
	channelID, consumerID := "itest-pause-chaos", "itest-pause-chaos-a"
	pauseTestChannel(channelID)
	pauseTestConsumer(channelID, consumerID, "")
	recorder := newDeliveryRecorder()
	consumerHandler[consumerID] = recorder.handler()
	var windowsMutex sync.Mutex
	windows := make([]pauseWindow, 0)
	stop := make(chan struct{})
	var togglers sync.WaitGroup
	toggle := func(pause, resume func(), on, off, initialDelay time.Duration) {
		defer togglers.Done()
		time.Sleep(initialDelay)
		for {
			select {
			case <-stop:
				return
			default:
			}
			pause()
			from := time.Now()
			time.Sleep(on)
			to := time.Now()
			resume()
			windowsMutex.Lock()
			windows = append(windows, pauseWindow{from: from, to: to})
			windowsMutex.Unlock()
			time.Sleep(off)
		}
	}
	togglers.Add(2)
	go toggle(func() { pauseConsumerAPI(channelID, consumerID) }, func() { resumeConsumerAPI(channelID, consumerID) }, 1500*time.Millisecond, 500*time.Millisecond, 0)
	go toggle(func() { globalPauseAPI(true) }, func() { globalPauseAPI(false) }, 2500*time.Millisecond, 1300*time.Millisecond, 700*time.Millisecond)
	broadcastDone := make(chan error, 1)
	go func() { broadcastDone <- broadcastMessage(channelID, 2000) }()
	time.Sleep(12 * time.Second)
	close(stop)
	togglers.Wait()
	exitIf(<-broadcastDone != nil, 141, "broadcast failed")
	resumeConsumerAPI(channelID, consumerID)
	globalPauseAPI(false)
	created := 0
	db.QueryRow("SELECT COUNT(*) FROM message WHERE channelId = ?", channelID).Scan(&created)
	exitIf(created != 2000, 141, "only %d/2000 broadcasts were accepted (see broadcast errors above)", created)
	exitIf(!waitFor(120*time.Second, func() bool { return recorder.unique() >= 2000 }), 142, "received %d/2000 after toggling", recorder.unique())
	exitIf(!waitFor(10*time.Second, func() bool { return parkedCount(db, channelID, consumerID) == 0 }), 143, "parking not empty after toggling")
	assertNoJobOverlap(db, 144)
	violations := 0
	for _, window := range windows {
		// deliveries already in flight when a pause began may still start within the first second
		violations += recorder.startsBetween(window.from.Add(time.Second), window.to)
	}
	exitIf(violations != 0, 145, "%d deliveries started inside paused windows", violations)
	log.Printf("Dispatch pause chaos test passed (%d pause windows, %d duplicate deliveries)\n", len(windows), recorder.duplicates())
}

// explainKeys returns table -> key and table -> Extra for each row of the MySQL EXPLAIN output
func explainKeys(db *sql.DB, query string, args ...interface{}) (map[string]string, map[string]string) {
	rows, err := db.Query("EXPLAIN "+query, args...)
	exitIf(err != nil, 146, "EXPLAIN failed: %v", err)
	defer rows.Close()
	columns, _ := rows.Columns()
	result, extras := map[string]string{}, map[string]string{}
	for rows.Next() {
		values := make([]sql.NullString, len(columns))
		pointers := make([]interface{}, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		exitIf(rows.Scan(pointers...) != nil, 146, "could not scan EXPLAIN output")
		table, key, extra := "", "", ""
		for index, column := range columns {
			switch column {
			case "table":
				table = values[index].String
			case "key":
				key = values[index].String
			case "Extra":
				extra = values[index].String
			}
		}
		result[table] = key
		extras[table] = extra
	}
	return result, extras
}

func testDispatchPauseQueryPlans() {
	log.Println("Starting dispatch pause: MySQL query plans")
	db := pauseTestDB()
	defer db.Close()
	// Several paused consumers so that consumerId is selective, as in production
	channelID, consumerID := "itest-pause-plans", "itest-pause-plans-a"
	consumerIDs := []string{consumerID, "itest-pause-plans-b", "itest-pause-plans-c", "itest-pause-plans-d"}
	pauseTestChannel(channelID)
	for _, id := range consumerIDs {
		pauseTestConsumer(channelID, id, "")
		pauseConsumerAPI(channelID, id)
	}
	exitIf(broadcastMessage(channelID, 100) != nil, 146, "broadcast failed")
	for _, id := range consumerIDs {
		parkedID := id
		exitIf(!waitFor(30*time.Second, func() bool { return parkedCount(db, channelID, parkedID) == 100 }), 146, "expected 100 parked jobs for %s", parkedID)
	}
	db.Exec("ANALYZE TABLE job_parking")
	var internalConsumerID string
	db.QueryRow("SELECT id FROM consumer WHERE channelId = ? AND consumerId = ?", channelID, consumerID).Scan(&internalConsumerID)
	checks := []struct {
		name, query, table, key string
		args                    []interface{}
	}{
		// The pause check takes the consumer id as a parameter so the index serves ORDER BY ... LIMIT (no filesort).
		// The consumers are paused while parked rows exist, so the pause parameter names an unpaused id; otherwise MySQL
		// evaluates the constant subquery first and reports "Impossible WHERE".
		{"drain page", "SELECT seq, id, messageId FROM job_parking p WHERE p.consumerId = ? AND NOT EXISTS (SELECT 1 FROM dispatch_pause d WHERE d.scope IN (?, '__global__')) ORDER BY p.createdAt, p.seq LIMIT 50", "p", "job_parking_drain", []interface{}{internalConsumerID, "not-paused-consumer"}},
		{"parked consumers", "SELECT DISTINCT consumerId FROM job_parking", "job_parking", "job_parking_drain", nil},
		// UNIQUE (messageId, consumerId) is named after its first column by MySQL
		{"prune exclusion", "SELECT m.id FROM message m WHERE m.status = 2 AND m.createdAt <= NOW() AND NOT EXISTS (SELECT 1 FROM job_parking p WHERE p.messageId = m.id)", "p", "messageId", nil},
		{"gated hand-out", "UPDATE job SET status = 1002, statusChangedAt = NOW(), updatedAt = NOW() WHERE id like 'nonexistent' and status = 1001 AND NOT EXISTS (SELECT 1 FROM dispatch_pause d WHERE d.scope IN (job.consumerId, '__global__'))", "job", "PRIMARY", nil},
	}
	failed := false
	for _, check := range checks {
		keys, extras := explainKeys(db, check.query, check.args...)
		if keys[check.table] != check.key || strings.Contains(extras[check.table], "filesort") {
			log.Printf("query plan check %q: table %s uses key %q (extra %q), expected %q without filesort; plan %v\n", check.name, check.table, keys[check.table], extras[check.table], check.key, keys)
			failed = true
		}
	}
	for _, id := range consumerIDs {
		resumeConsumerAPI(channelID, id)
	}
	for _, id := range consumerIDs {
		parkedID := id
		waitFor(30*time.Second, func() bool { return parkedCount(db, channelID, parkedID) == 0 })
	}
	exitIf(failed, 147, "query plans do not use the expected indexes")
	log.Println(fmt.Sprintf("Dispatch pause query plan test passed"))
}

// Generated with assistance from Claude AI
