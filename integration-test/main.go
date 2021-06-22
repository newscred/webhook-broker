package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
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
	channelID                      = "integration-test-channel"
	producerID                     = "integration-test-producer"
	consumerIDPrefix               = "integration-test-consumer-"
	formDataContentTypeHeaderValue = "application/x-www-form-urlencoded"
	headerContentType              = "Content-Type"
	headerUnmodifiedSince          = "If-Unmodified-Since"
	headerLastModified             = "Last-Modified"
	headerChannelToken             = "X-Broker-Channel-Token"
	headerProducerToken            = "X-Broker-Producer-Token"
	headerProducerID               = "X-Broker-Producer-ID"
	consumerCount                  = 5
	payload                        = `{"test":"hello world"}`
	contentType                    = "application/json"
	concurrentPushWorkers          = 50
	maxMessages                    = 1000000
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
	formValues.Add(tokenFormParamKey, token)
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

func createChannel() (err error) {
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

type MsgStakeholder struct {
	ID        string
	Name      string
	Token     string
	ChangedAt time.Time
}

func updateChannel() (err error) {
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

func createConsumers(baseURI string) int {
	for index := 0; index < consumerCount; index++ {
		indexString := strconv.Itoa(index)
		formValues := url.Values{}
		formValues.Add(tokenFormParamKey, token)
		url := baseURI + "/" + consumerIDPrefix + indexString
		log.Println("callback url", url)
		formValues.Add(callbackURLFormParamKey, url)
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
			respBody, _ := ioutil.ReadAll(resp.Body)
			log.Println("response", resp.Status, string(respBody))
			return 0
		}
	}
	return consumerCount
}

func broadcastMessage(sendCount int) (err error) {
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
			if resp.StatusCode != http.StatusAccepted {
				respBody, _ := ioutil.ReadAll(resp.Body)
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
	for index := 0; index < consumerCount; index++ {
		consumerHandler[consumerIDPrefix+strconv.Itoa(index)] = func(s string, rw http.ResponseWriter, r *http.Request) {
			defer func() {
				if r := recover(); r != nil {
					log.Println("Recovered", r)
				}
			}()
			if assert {
				body, _ := ioutil.ReadAll(r.Body)
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
	testMessageTransmission()
	resetHandlers()
	testDLQFlow()
}

func testBasicObjectCreation(portString string) {
	var count = consumerCount
	var err error
	err = createProducer()
	if err != nil {
		log.Println("error creating producer", err)
		return
	}
	err = createChannel()
	if err != nil {
		log.Println("error creating channel", err)
		return
	}
	err = updateChannel()
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
		wg := addConsumerVerified(step*consumerCount+failures, true, failures)
		err := broadcastMessage(step)
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

func testDLQFlow() {
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
		body, _ := ioutil.ReadAll(r.Body)
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
	err := broadcastMessage(1)
	if err != nil {
		log.Println("error broadcasting message", err)
		os.Exit(7)
	}
	timeoutDuration := (1 + 2 + 3 + 4 + 5 + 10) * time.Second
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
	body, _ := ioutil.ReadAll(resp.Body)
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
		body, _ := ioutil.ReadAll(r.Body)
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
