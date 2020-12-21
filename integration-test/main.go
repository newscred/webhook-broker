package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/julienschmidt/httprouter"
)

var (
	consumerHandler map[string]func(string, http.ResponseWriter, *http.Request)
	server          *http.Server
	client          *http.Client
)

const (
	consumerHostName               = "tester"
	brokerBaseURL                  = "http://webhook-broker:8080"
	token                          = "someRandomToken"
	tokenFormParamKey              = "token"
	channelID                      = "integration-test-channel"
	producerID                     = "integration-test-producer"
	consumerIDPrefix               = "integration-test-consumer-"
	formDataContentTypeHeaderValue = "application/x-www-form-urlencoded"
	headerContentType              = "Content-Type"
	consumerCount                  = 5
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
	if customController, ok := consumerHandler[consumerID]; ok {
		customController(consumerID, w, r)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

func createProducer() {
	req, _ := http.NewRequest(http.MethodPut, brokerBaseURL+"/producer/"+producerID, nil)
	req.Header.Add(headerContentType, formDataContentTypeHeaderValue)
	req.PostForm = url.Values{}
	req.PostForm.Add(tokenFormParamKey, token)
	client.Do(req)
}
func createChannel()                                  {}
func createConsumers(baseURI string) int              { return 0 }
func broadcastMessage() (payload, contentType string) { return payload, contentType }
func addConsumerVerified(payload, contentType string, consumerCount int) *sync.WaitGroup {
	return &sync.WaitGroup{}
}

func main() {
	consumerHandler = make(map[string]func(string, http.ResponseWriter, *http.Request))
	client = &http.Client{Timeout: 2 * time.Second}
	port := findPort()
	if port == 0 {
		log.Fatalln("could not find port to start test consumer service")
	}
	portString := ":" + strconv.Itoa(port)
	baseURLString := "http://" + consumerHostName + portString
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
	createProducer()
	createChannel()
	count := createConsumers(baseURLString)
	log.Println("number of consumers created", count)
	payload, contentType := broadcastMessage()
	wg := addConsumerVerified(payload, contentType, count)
	wg.Wait()
	defer func() {
		serverShutdownContext, shutdownTimeoutCancelFunc := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutdownTimeoutCancelFunc()
		server.Shutdown(serverShutdownContext)
	}()
}
