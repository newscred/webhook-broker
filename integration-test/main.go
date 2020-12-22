package main

import (
	"context"
	"errors"
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

	"github.com/julienschmidt/httprouter"
)

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
	headerChannelToken             = "X-Broker-Channel-Token"
	headerProducerToken            = "X-Broker-Producer-Token"
	headerProducerID               = "X-Broker-Producer-ID"
	consumerCount                  = 5
	payload                        = `{"test":"hello world"}`
	contentType                    = "application/json"
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
	log.Println("Received message!", consumerID)
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
	req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
	var resp *http.Response
	resp, err = client.Do(req)
	if resp.StatusCode != 200 {
		err = errDuringCreation
	}
	return err
}
func createChannel() (err error) {
	formValues := url.Values{}
	formValues.Add(tokenFormParamKey, token)
	req, _ := http.NewRequest(http.MethodPut, brokerBaseURL+"/channel/"+channelID, strings.NewReader(formValues.Encode()))
	req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
	var resp *http.Response
	resp, err = client.Do(req)
	if resp.StatusCode != 200 {
		err = errDuringCreation
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
		req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
		var resp *http.Response
		var err error
		resp, err = client.Do(req)
		if err != nil {
			log.Println("error creating consumer", err)
			return 0
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			respBody, _ := ioutil.ReadAll(resp.Body)
			log.Println("response", resp.Status, string(respBody))
			return 0
		}
	}
	return consumerCount
}
func broadcastMessage(sendCount int) (err error) {
	sendFn := func() {
		req, _ := http.NewRequest(http.MethodPost, brokerBaseURL+"/channel/"+channelID+"/broadcast", strings.NewReader(payload))
		req.Header.Add(headerContentType, contentType)
		req.Header.Add(headerChannelToken, token)
		req.Header.Add(headerProducerID, producerID)
		req.Header.Add(headerProducerToken, token)
		var resp *http.Response
		resp, err = client.Do(req)
		if err != nil {
			log.Println("error creating consumer", err)
		} else if resp.StatusCode != http.StatusAccepted {
			respBody, _ := ioutil.ReadAll(resp.Body)
			log.Println("error broadcasting message", resp.StatusCode, string(respBody))
			err = errDuringCreation
		}
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
		for index := 0; index < 50; index++ {
			go asyncSend()
		}
		for index := 0; index < sendCount; index++ {
			sendChan <- index
		}
	}
	return err
}
func addConsumerVerified(expectedEventCount int, assert bool) *sync.WaitGroup {
	wg := &sync.WaitGroup{}
	wg.Add(expectedEventCount)
	for index := 0; index < expectedEventCount; index++ {
		consumerHandler[consumerIDPrefix+strconv.Itoa(index)] = func(s string, rw http.ResponseWriter, r *http.Request) {
			if assert {
				defer r.Body.Close()
				body, _ := ioutil.ReadAll(r.Body)
				if string(body) != payload {
					consumerAssertionFailed = true
				}
				if r.Header.Get(headerContentType) != contentType {
					consumerAssertionFailed = true
				}
			}
			rw.WriteHeader(http.StatusNoContent)
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

func main() {
	consumerHandler = make(map[string]func(string, http.ResponseWriter, *http.Request))
	client = &http.Client{Timeout: 2 * time.Second}
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
	baseURLString := "http://" + consumerHostName + portString
	count = createConsumers(baseURLString)
	log.Println("number of consumers created", count)
	if count == 0 {
		log.Println("error creating consumers")
		return
	}
	log.Println("Starting message broadcast", time.Now())
	defaultMax := 1
	steps := []int{1, 10, 100, 500, 1000, 2500, 5000, 10000, 100000, 1000000}
	for _, step := range steps {
		if step > defaultMax {
			continue
		}
		wg := addConsumerVerified(step*count, true)
		err := broadcastMessage(step)
		if err != nil {
			log.Println("error broadcasting message", err)
			return
		}
		if waitTimeout(wg, time.Duration(2*step)*time.Second) {
			log.Println("Timed out waiting for wait group")
			os.Exit(2)
		} else {
			log.Println("Wait group finished", step, time.Now())
			if consumerAssertionFailed {
				log.Println("Consumer assertion failed")
				os.Exit(3)
			}
		}
	}

	defer func() {
		serverShutdownContext, shutdownTimeoutCancelFunc := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutdownTimeoutCancelFunc()
		server.Shutdown(serverShutdownContext)
	}()
}
