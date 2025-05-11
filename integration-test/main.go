package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/influxdata/tdigest"
	"github.com/julienschmidt/httprouter"
	"github.com/rs/xid"

	"database/sql"

	_ "github.com/go-sql-driver/mysql" // MySQL driver
)

type deadDeliveryJobModel struct {
	ListenerEndpoint string
	ListenerName     string
	Status           string
	StatusChangedAt  time.Time
	MessageURL       string
}

// DLQList represents the list of jobs that are dead
type dlqList struct {
	DeadJobs []*deadDeliveryJobModel
	Pages    map[string]string
}

type ConsumerModel struct {
	MsgStakeholder
	CallbackURL        string
	DeadLetterQueueURL string
	Type               string
}

type QeuedMessageModel struct {
	MessageID   string
	Payload     string
	ContentType string
	Priority    uint
}

type QueuedDeliveryJobModel struct {
	ID      xid.ID
	Message QeuedMessageModel
}

type JobListResult struct {
	Result []QueuedDeliveryJobModel
}

type JobStateUpdateModel struct {
	NextState          string
	IncrementalTimeout uint
}

var (
	consumerHandler         map[string]func(string, http.ResponseWriter, *http.Request)
	server                  *http.Server
	client                  *http.Client
	errDuringCreation       = errors.New("error during creating fixture")
	consumerAssertionFailed = false
)

const (
	consumerHostName               = "tester"
	brokerBaseURL                  = "http://webhook-broker:8080"
	token                          = "someRandomToken"
	tokenFormParamKey              = "token"
	callbackURLFormParamKey        = "callbackUrl"
	consumerTypeFormParamKey       = "type"
	generalChannelID               = "integration-test-general-channel"
	pullChannelID                  = "integration-test-pull-channel"
	producerID                     = "integration-test-producer"
	consumerIDPrefix               = "integration-test-consumer-"
	formDataContentTypeHeaderValue = "application/x-www-form-urlencoded"
	headerContentType              = "Content-Type"
	headerUnmodifiedSince          = "If-Unmodified-Since"
	headerLastModified             = "Last-Modified"
	headerChannelToken             = "X-Broker-Channel-Token"
	headerProducerToken            = "X-Broker-Producer-Token"
	headerProducerID               = "X-Broker-Producer-ID"
	headerConsumerToken            = "X-Broker-Consumer-Token"
	pushConsumerCount              = 5
	pullConsumerCount              = 2
	payload                        = `{"test":"hello world"}`
	contentType                    = "application/json"
	concurrentPushWorkers          = 50
	maxMessages                    = 1000000
	JobInflightState               = "INFLIGHT"
	JobDeliveredState              = "DELIVERED"
)

func findPort() int {
	for port := 61500; port < 63000; port++ {
		if checkPort(port) == nil {
			return port
		}
	}
	return 0
}

func checkPort(port int) (err error) {
	ln, netErr := net.Listen("tcp", ":"+strconv.Itoa(port))
	defer ln.Close()
	if netErr != nil {
		log.Println(netErr)
		err = netErr
	}
	return err
}

func connectToMySQL(connectionString string) (*sql.DB, error) {
	db, err := sql.Open("mysql", connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MySQL: %w", err)
	}

	return db, nil
}

func consumerController(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	consumerID := params.ByName("consumerId")
	defer r.Body.Close()
	if customController, ok := consumerHandler[consumerID]; ok {
		customController(consumerID, w, r)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

func createProducer() (err error) {
	formValues := url.Values{}
	formValues.Add(tokenFormParamKey, token+"NEW")
	req, _ := http.NewRequest(http.MethodPut, brokerBaseURL+"/producer/"+producerID, strings.NewReader(formValues.Encode()))
	defer req.Body.Close()
	req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
	var resp *http.Response
	resp, err = client.Do(req)
	if err == nil {
		defer resp.Body.Close()
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		err = errDuringCreation
	}
	return err
}

func updateProducer() (err error) {
	gReq, _ := http.NewRequest(http.MethodGet, brokerBaseURL+"/producer/"+producerID, nil)
	gResp, err := client.Do(gReq)
	if err != nil {
		log.Println(err)
		return err
	} else {
		defer gResp.Body.Close()
	}
	formValues := url.Values{}
	formValues.Add(tokenFormParamKey, token)
	req, _ := http.NewRequest(http.MethodPut, brokerBaseURL+"/producer/"+producerID, strings.NewReader(formValues.Encode()))
	defer req.Body.Close()
	req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
	req.Header.Add(headerUnmodifiedSince, gResp.Header.Get(headerLastModified))
	var resp *http.Response
	resp, err = client.Do(req)
	if err == nil {
		defer resp.Body.Close()
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		err = errDuringCreation
	}
	return err
}

func createChannel(channelID string) (err error) {
	formValues := url.Values{}
	formValues.Add(tokenFormParamKey, token+"NEW")
	req, _ := http.NewRequest(http.MethodPut, brokerBaseURL+"/channel/"+channelID, strings.NewReader(formValues.Encode()))
	defer req.Body.Close()
	req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
	var resp *http.Response
	resp, err = client.Do(req)
	if err == nil {
		defer resp.Body.Close()
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		err = errDuringCreation
	}
	return err
}

func createChannels() (err error) {
	generalChannelErr := createChannel(generalChannelID)
	pushChannelErr := createChannel(pullChannelID)
	if generalChannelErr != nil || pushChannelErr != nil {
		err = errDuringCreation
	}
	return err
}

type MsgStakeholder struct {
	ID        string
	Name      string
	Token     string
	ChangedAt time.Time
}

func updateChannel(channelID string) (err error) {
	gReq, _ := http.NewRequest(http.MethodGet, brokerBaseURL+"/channel/"+channelID, nil)
	gResp, err := client.Do(gReq)
	if err != nil {
		log.Println(err)
		return err
	} else {
		defer gResp.Body.Close()
	}
	var data MsgStakeholder
	reqBody, err := io.ReadAll(gResp.Body)
	if err != nil {
		log.Println(err)
		return err
	}
	log.Println(string(reqBody))
	err = json.Unmarshal(reqBody, &data)
	if err != nil {
		log.Println(err)
		return err
	}
	if data.ChangedAt.Format(http.TimeFormat) != gResp.Header.Get(headerLastModified) {
		log.Fatal("Changed at and last modified not same - ", data.ChangedAt.Format(http.TimeFormat), " vs ", gResp.Header.Get(headerLastModified))
	}
	formValues := url.Values{}
	formValues.Add(tokenFormParamKey, token)
	req, _ := http.NewRequest(http.MethodPut, brokerBaseURL+"/channel/"+channelID, strings.NewReader(formValues.Encode()))
	defer req.Body.Close()
	req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
	req.Header.Add(headerUnmodifiedSince, data.ChangedAt.Format(http.TimeFormat))
	var resp *http.Response
	resp, err = client.Do(req)
	if err == nil {
		defer resp.Body.Close()
	} else {
		log.Println(err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		err = errDuringCreation
		body, _ := io.ReadAll(resp.Body)
		log.Println(resp.Status, string(body))
	}
	return err
}

func updateChannels() (err error) {
	generalChannelErr := updateChannel(generalChannelID)
	pushChannelErr := updateChannel(pullChannelID)
	if generalChannelErr != nil || pushChannelErr != nil {
		err = errDuringCreation
	}
	return err
}

func createConsumers(baseURI string) int {
	for index := 0; index < pushConsumerCount+pullConsumerCount; index++ {
		var channelID string
		if index < pushConsumerCount {
			channelID = generalChannelID
		} else {
			channelID = pullChannelID
		}
		indexString := strconv.Itoa(index)
		formValues := url.Values{}
		formValues.Add(tokenFormParamKey, token)
		url := baseURI + "/" + consumerIDPrefix + indexString
		log.Println("callback url", url)
		formValues.Add(callbackURLFormParamKey, url)
		if index >= pushConsumerCount {
			formValues.Add(consumerTypeFormParamKey, "pull")
		}
		req, _ := http.NewRequest(http.MethodPut, brokerBaseURL+"/channel/"+channelID+"/consumer/"+consumerIDPrefix+indexString, strings.NewReader(formValues.Encode()))
		defer req.Body.Close()
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		var resp *http.Response
		var err error
		resp, err = client.Do(req)
		if err != nil {
			log.Println("error creating consumer", err)
			return 0
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
			respBody, _ := io.ReadAll(resp.Body)
			log.Println("response", resp.Status, string(respBody))
			return 0
		}
	}
	return pushConsumerCount + pullConsumerCount
}

func broadcastMessage(channelID string, sendCount int) (err error) {
	td := tdigest.NewWithCompression(maxMessages)
	batchStart := time.Now()
	var wg sync.WaitGroup
	wg.Add(sendCount)
	sendFn := func() {
		start := time.Now()
		req, _ := http.NewRequest(http.MethodPost, brokerBaseURL+"/channel/"+channelID+"/broadcast", strings.NewReader(payload))
		defer req.Body.Close()
		req.Header.Add(headerContentType, contentType)
		req.Header.Add(headerChannelToken, token)
		req.Header.Add(headerProducerID, producerID)
		req.Header.Add(headerProducerToken, token)
		var resp *http.Response
		resp, err = client.Do(req)
		if err != nil {
			log.Println("error broadcasting to consumers", err)
		} else {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusCreated {
				respBody, _ := io.ReadAll(resp.Body)
				log.Println("error broadcasting message", resp.StatusCode, string(respBody))
				err = errDuringCreation
			}
		}
		td.Add(float64(time.Since(start).Milliseconds()), 1)
		wg.Done()
	}
	switch {
	case sendCount == 1:
		sendFn()
	case sendCount <= 10:
		for index := 0; index < sendCount; index++ {
			sendFn()
		}
	default:
		sendChan := make(chan int, sendCount)
		asyncSend := func() {
			for {
				select {
				case <-sendChan:
					sendFn()
				}
			}
		}
		for index := 0; index < concurrentPushWorkers; index++ {
			go asyncSend()
		}
		for index := 0; index < sendCount; index++ {
			sendChan <- index
		}
	}
	wg.Wait()
	log.Println(fmt.Sprintf("Dispatched %d messages in %s; per message - average: %f, 75th percentile: %f, 95th percentile: %f, 99th percentile: %f", sendCount, time.Since(batchStart), td.Quantile(0.5), td.Quantile(0.75), td.Quantile(0.95), td.Quantile(0.99)))
	return err
}
func addConsumerVerified(expectedEventCount int, assert bool, simulateFailures int) *sync.WaitGroup {
	wg := &sync.WaitGroup{}
	wg.Add(expectedEventCount)
	failuresLeft := simulateFailures
	for index := 0; index < pushConsumerCount; index++ {
		consumerHandler[consumerIDPrefix+strconv.Itoa(index)] = func(s string, rw http.ResponseWriter, r *http.Request) {
			defer func() {
				if r := recover(); r != nil {
					log.Println("Recovered", r)
				}
			}()
			if assert {
				body, _ := io.ReadAll(r.Body)
				if string(body) != payload {
					consumerAssertionFailed = true
					log.Println("error - assertion failed for", s)
				}
				if r.Header.Get(headerContentType) != contentType {
					consumerAssertionFailed = true
					log.Println("error - assertion failed for", s)
				}
			}
			if failuresLeft > 0 {
				failuresLeft--
				log.Println("SENDING FAILURE")
				rw.WriteHeader(http.StatusNotFound)
			} else {
				rw.WriteHeader(http.StatusNoContent)
			}
			wg.Done()
		}
	}
	return wg
}

func waitTimeout(wg *sync.WaitGroup, timeout time.Duration) bool {
	c := make(chan struct{})
	go func() {
		defer close(c)
		wg.Wait()
	}()
	select {
	case <-c:
		return false // completed normally
	case <-time.After(timeout):
		return true // timed out
	}
}

func resetHandlers() {
	consumerHandler = make(map[string]func(string, http.ResponseWriter, *http.Request))
}

func main() {
	client = &http.Client{Timeout: 2 * time.Second}
	http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = concurrentPushWorkers
	port := findPort()
	if port == 0 {
		log.Fatalln("could not find port to start test consumer service")
	}
	portString := ":" + strconv.Itoa(port)
	testConsumerRouter := httprouter.New()
	testConsumerRouter.POST("/:consumerId", consumerController)
	server = &http.Server{
		Handler: testConsumerRouter,
		Addr:    portString,
	}
	go func() {
		if serverListenErr := server.ListenAndServe(); serverListenErr != nil {
			log.Println(serverListenErr)
		}
	}()
	defer func() {
		serverShutdownContext, shutdownTimeoutCancelFunc := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutdownTimeoutCancelFunc()
		server.Shutdown(serverShutdownContext)
	}()
	testBasicObjectCreation(portString)
	resetHandlers()
	testConsumerTypeCreation(portString)
	resetHandlers()
	testMessageTransmission()
	resetHandlers()
	testJobPullFlow()
	resetHandlers()
	testDLQFlow()
	resetHandlers()
	testPruning()
}

func testBasicObjectCreation(portString string) {
	var count = pushConsumerCount + pullConsumerCount
	var err error
	err = createProducer()
	if err != nil {
		log.Println("error creating producer", err)
		return
	}
	err = updateProducer()
	if err != nil {
		log.Println("error updating producer", err)
		return
	}
	err = createChannels()
	if err != nil {
		log.Println("error creating channel", err)
		return
	}
	err = updateChannels()
	if err != nil {
		log.Println("error updating channel", err)
		return
	}
	baseURLString := "http://" + consumerHostName + portString
	count = createConsumers(baseURLString)
	log.Println("number of consumers created", count)
	if count == 0 {
		log.Println("error creating consumers")
		os.Exit(4)
	}
}

func testConsumerTypeCreation(portString string) {
	baseURLString := "http://" + consumerHostName + portString
	consumerCreated := 0
	channelID := generalChannelID

	var tests = []struct {
		description          string
		passedConsumerType   string
		expectedConsumerType string
	}{
		{"default consumer", "", "push"},
		{"push consumer", "push", "push"},
		{"pull consumer", "pull", "pull"},
		{"wrong consumer", "wrongType", ""},
	}

	for index, tt := range tests {
		log.Println(".......", tt.description, ".......")
		indexString := strconv.Itoa(index + 100)
		formValues := url.Values{}
		formValues.Add(tokenFormParamKey, token)
		url := baseURLString + "/" + consumerIDPrefix + indexString
		log.Println("callback url", url)
		formValues.Add(callbackURLFormParamKey, url)
		log.Println("Passed ConsumerType", tt.passedConsumerType)
		formValues.Add(consumerTypeFormParamKey, tt.passedConsumerType)
		req, _ := http.NewRequest(http.MethodPut, brokerBaseURL+"/channel/"+channelID+"/consumer/"+consumerIDPrefix+indexString, strings.NewReader(formValues.Encode()))
		defer req.Body.Close()
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		var resp *http.Response
		var err error
		resp, err = client.Do(req)
		if err != nil {
			log.Println("error creating consumer", err)
			continue
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		log.Println("response", resp.Status, string(respBody))

		if tt.passedConsumerType == "wrongType" {
			// must return bad request for invalid consumer type
			if resp.StatusCode != http.StatusBadRequest {
				log.Println("Error: invalid status code for wrong consumer type")
				os.Exit(24)
			}
			continue
		}

		if resp.StatusCode != http.StatusOK {
			log.Println("Consumer Creation failed")
			continue
		}
		var data ConsumerModel
		err = json.Unmarshal(respBody, &data)
		if err != nil {
			log.Println(err)
			continue
		}
		if data.Type != tt.expectedConsumerType {
			log.Println("Wrong Consumer Type", data.Type, tt.expectedConsumerType)
			continue
		}
		consumerCreated++
	}

	log.Println("number of consumers created", consumerCreated)
	if consumerCreated != 3 {
		log.Println("error creating consumers")
		os.Exit(4)
	}
}

func testMessageTransmission() {
	log.Println("Starting message broadcast", time.Now())
	defaultMax := 10000
	steps := []int{1, 10, 100, 500, 1000, 2500, 5000, 10000, 100000, maxMessages}
	failures := 2
	for _, step := range steps {
		if step > defaultMax {
			continue
		}
		start := time.Now()
		wg := addConsumerVerified(step*pushConsumerCount+failures, true, failures)
		err := broadcastMessage(generalChannelID, step)
		if err != nil {
			log.Println("error broadcasting message", err)
			os.Exit(1)
		}
		timeoutDuration := time.Duration(2*step)*time.Second + time.Duration(failures)*time.Second*4
		if waitTimeout(wg, timeoutDuration) {
			log.Println("Timed out waiting for wait group after", timeoutDuration)
			os.Exit(2)
		} else {
			end := time.Now()
			log.Println("Wait group finished", step, end)
			log.Println("Batch Duration", step, end.Sub(start))
			if consumerAssertionFailed {
				log.Println("Consumer assertion failed")
				os.Exit(3)
			}
		}
	}
}

func testJobPullFlow() {
	log.Println("beginning pull consumers workflow testing")
	step, limit := 40, 25
	log.Println("Starting message broadcast for pull consumers", time.Now())
	broadcastMessage(pullChannelID, step)

	getQueuedJobs := func(channelID string, consumerID string, limit int) (queuedJobs JobListResult) {
		q_url := brokerBaseURL + "/channel/" + channelID + "/consumer/" + consumerID + "/queued-jobs"
		req, _ := http.NewRequest(http.MethodGet, q_url, nil)
		q := url.Values{}
		q.Add("limit", strconv.Itoa(limit))
		req.URL.RawQuery = q.Encode()
		req.Header.Add(headerChannelToken, token)
		req.Header.Add(headerConsumerToken, token)
		resp, err := client.Do(req)
		if err != nil {
			log.Println(err)
			os.Exit(14)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(body, &queuedJobs)
		if err != nil {
			log.Println(err)
			os.Exit(15)
		}
		return
	}

	updateJobState := func(channelID string, consumerID string, jobID xid.ID, state string, timeout uint) (success bool) {
		url := brokerBaseURL + "/channel/" + channelID + "/consumer/" + consumerID + "/job/" + jobID.String()
		body := JobStateUpdateModel{NextState: state, IncrementalTimeout: timeout}
		jsonBody, _ := json.Marshal(body)
		bodyReader := bytes.NewBuffer(jsonBody)
		req, _ := http.NewRequest(http.MethodPost, url, bodyReader)
		req.Header.Add(headerChannelToken, token)
		req.Header.Add(headerConsumerToken, token)
		resp, err := client.Do(req)
		if err != nil {
			log.Println(err)
			os.Exit(16)
		}

		if resp.StatusCode == http.StatusAccepted {
			success = true
		} else if resp.StatusCode == http.StatusBadRequest {
			success = false
		} else {
			log.Println("unknown status for state update for job", jobID)
			os.Exit(17)
		}
		return
	}

	channelID := pullChannelID
	jobIDs := make([]xid.ID, limit)
	time.Sleep(5 * time.Second)
	for index := pushConsumerCount; index < pushConsumerCount+pullConsumerCount; index++ {
		indexString := strconv.Itoa(index)
		consumerID := consumerIDPrefix + indexString
		data := getQueuedJobs(channelID, consumerID, limit)
		if len(data.Result) != limit {
			log.Println(fmt.Sprintf("queued jobs count mismatch for consumer %d vs %d", len(data.Result), limit), consumerID)
			os.Exit(18)
		}
		for i := 0; i < limit; i++ {
			jobIDs[i] = data.Result[i].ID
		}
	}
	firstConsumerID := consumerIDPrefix + strconv.Itoa(pushConsumerCount)
	lastConsumerID := consumerIDPrefix + strconv.Itoa(pushConsumerCount+pullConsumerCount-1)
	longTimeout := 10 // in seconds
	for _, jobID := range jobIDs {
		success := updateJobState(channelID, lastConsumerID, jobID, JobInflightState, uint(longTimeout))
		if !success {
			log.Println("failed to update status from queued to inflight for job", jobID)
			os.Exit(19)
		}
	}
	time.Sleep(3 * time.Second)
	data := getQueuedJobs(channelID, firstConsumerID, limit)
	if len(data.Result) != limit {
		log.Println("received queued-job count mismatch for consumer,", firstConsumerID, "after state update")
		os.Exit(19)
	}
	data = getQueuedJobs(channelID, lastConsumerID, limit)
	if len(data.Result) != int(math.Min(float64(limit), float64(step-limit))) {
		log.Println("received queued-job count mismatch for consumer,", lastConsumerID, "after state update")
		os.Exit(20)
	}
	firstJobID := jobIDs[0]
	success := updateJobState(channelID, lastConsumerID, firstJobID, JobDeliveredState, 0)
	if !success {
		log.Println("failed to update status from inflight to delivered for job", firstJobID)
		os.Exit(21)
	}
	time.Sleep(3 * time.Second)
	success = updateJobState(channelID, lastConsumerID, firstJobID, JobInflightState, 0)
	if success {
		log.Println("wrongly updated status from delivered to inFlight for job", firstJobID)
		os.Exit(21)
	}
	time.Sleep(7 * time.Second)
	data = getQueuedJobs(channelID, lastConsumerID, step)
	if len(data.Result) != step-limit {
		log.Println("Job requeued earlier for consumer ", lastConsumerID, len(data.Result))
		os.Exit(22)
	}
	time.Sleep(15 * time.Second)
	data = getQueuedJobs(channelID, lastConsumerID, step)
	// Should expect step - 1 jobs in queued, because one got delivered.
	if len(data.Result) != step-1 {
		log.Println("Long Inflight jobs did not get requeued for comumer ", lastConsumerID, " after timeout", len(data.Result))
		os.Exit(23)
	}
	log.Println("pull consumers workflow testing successfully finished!")
}

func testDLQFlow() {
	channelID := generalChannelID
	start := time.Now()
	wg := &sync.WaitGroup{}
	wg.Add(6)
	indexString := "0"
	consumerHandler[consumerIDPrefix+indexString] = func(s string, rw http.ResponseWriter, r *http.Request) {
		defer func() {
			if r := recover(); r != nil {
				log.Println("Recovered in DLQ Flow", r)
			}
		}()
		body, _ := io.ReadAll(r.Body)
		if string(body) != payload {
			consumerAssertionFailed = true
			log.Println("error - assertion failed for", s)
		}
		if r.Header.Get(headerContentType) != contentType {
			consumerAssertionFailed = true
			log.Println("error - assertion failed for", s)
		}
		rw.WriteHeader(http.StatusNotFound)
		wg.Done()
	}
	err := broadcastMessage(generalChannelID, 1)
	if err != nil {
		log.Println("error broadcasting message", err)
		os.Exit(7)
	}
	timeoutDuration := (1 + 2 + 3 + 4 + 5 + 20) * time.Second
	if waitTimeout(wg, timeoutDuration) {
		log.Println("Timed out waiting for wait group after", timeoutDuration)
		os.Exit(5)
	} else {
		end := time.Now()
		log.Println("Wait group finished dead messages", end)
		log.Println("Dead Duration", end.Sub(start))
		if consumerAssertionFailed {
			log.Println("Consumer assertion failed")
			os.Exit(6)
		}
	}
	time.Sleep(500 * time.Millisecond)
	// Ensure DLQ has this message
	dlqURL := brokerBaseURL + "/channel/" + channelID + "/consumer/" + consumerIDPrefix + indexString + "/dlq"
	req, _ := http.NewRequest(http.MethodGet, dlqURL, nil)
	var resp *http.Response
	resp, err = client.Do(req)
	if err != nil {
		log.Println(err)
		os.Exit(8)
	}
	body, _ := io.ReadAll(resp.Body)
	log.Println("BODY", string(body))
	decoder := json.NewDecoder(bytes.NewBuffer(body))
	dlq := &dlqList{}
	err = decoder.Decode(dlq)
	if err != nil {
		log.Println(err)
		os.Exit(9)
	}
	if len(dlq.DeadJobs) != 1 {
		log.Println("DLQ List mismatch", dlq)
		os.Exit(10)
	}
	// POST to requeue DLQ
	start = time.Now()
	formValues := url.Values{}
	formValues.Add("requeue", token)
	req, _ = http.NewRequest(http.MethodPost, dlqURL, strings.NewReader(formValues.Encode()))
	req.Header.Set(headerContentType, formDataContentTypeHeaderValue)
	wg.Add(1)
	consumerHandler[consumerIDPrefix+indexString] = func(s string, rw http.ResponseWriter, r *http.Request) {
		defer func() {
			if r := recover(); r != nil {
				log.Println("Recovered", r)
			}
		}()
		body, _ := io.ReadAll(r.Body)
		if string(body) != payload {
			consumerAssertionFailed = true
			log.Println("error - assertion failed for", s)
		}
		if r.Header.Get(headerContentType) != contentType {
			consumerAssertionFailed = true
			log.Println("error - assertion failed for", s)
		}
		rw.WriteHeader(http.StatusOK)
		wg.Done()
	}
	resp, err = client.Do(req)
	if err != nil {
		log.Println(err)
		os.Exit(11)
	}
	if waitTimeout(wg, timeoutDuration) {
		log.Println("Timed out waiting for wait group after", timeoutDuration)
		os.Exit(12)
	} else {
		end := time.Now()
		log.Println("Wait group finished dead recovery", end)
		log.Println("Dead Recovery Duration", end.Sub(start))
		if consumerAssertionFailed {
			log.Println("Consumer assertion failed")
			os.Exit(13)
		}
	}
}

func testPruning() {
	// Subtract message created date by 2 days
	// TODO Copied from the integration test configuration, consider rather reading it from the config file directly to avoid copying of the URL
	connectionString := "webhook_broker:zxc909zxc@tcp(mysql:3306)/webhook-broker?charset=utf8mb4&collation=utf8mb4_0900_ai_ci&parseTime=true&multiStatements=true"
	db, err := connectToMySQL(connectionString)
	if err != nil {
		log.Fatal(err) // Handle error appropriately
	}
	defer db.Close()
	// Check messages count - pre-pruning
	var messageCount int
	err = db.QueryRow("SELECT COUNT(*) FROM message").Scan(&messageCount)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Number of messages before prune %d\n", messageCount)
	_, err = db.Exec("UPDATE message SET createdAt = DATE_SUB(createdAt, INTERVAL 2 DAY), receivedAt = DATE_SUB(receivedAt, INTERVAL 2 DAY)")
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Updated message createdAt to -2 days")
	_, err = db.Exec("UPDATE job SET status = 1003 WHERE status = 1001 AND consumerId IN (SELECT id FROM consumer WHERE type = 0);")
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Pull jobs converted to delivered")
	// Sleep for the pruning to take place
	time.Sleep(15 * time.Second)
	// Check messages count - post pruning or pruning in progress
	var postPruningMessageCount int
	err = db.QueryRow("SELECT COUNT(*) FROM message").Scan(&postPruningMessageCount)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Number of messages after prune started %d\n", postPruningMessageCount)
	if postPruningMessageCount >= messageCount {
		log.Println("No messages have been deleted from pruning even after waiting 30s+")
		os.Exit(31)
	}
	// Check export links
	prunerURL := "http://webhook-broker-pruner/"
	resp, err := client.Get(prunerURL) // Use the existing client for the call
	if err != nil {
		log.Fatal("Error calling pruner endpoint:", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatal("Error reading pruner response:", err)
	}

	if !strings.Contains(string(body), "w7b6_intg_test") {
		log.Fatal("Expected string 'w7b6_intg_test' not found in pruner response")
	}
	//TODO Check for the contents of the file to ensure it is exporting the right thing in right format
}
