package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DBEngineName is string alias for DB Engine Name
type DBEngineName string

// NewDBEngineName creates new DB Engine Name
func NewDBEngineName() DBEngineName {
	return "mysql"
}

func main() {
	engineName := InitializeDBEngineName()

	dispatcher := InitializeDispatcher()

	dispatcher.Run()
	fmt.Println(engineName)

	signals := make(chan os.Signal, 1)
	done := make(chan bool, 1)

	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-signals
		fmt.Println()
		fmt.Println(sig)
		done <- true
	}()

	server := configureWebAPI()
	go func() {
		if serverListenErr := server.ListenAndServe(); serverListenErr != nil {
			fmt.Println(serverListenErr)
		}
	}()

	time.Sleep(2 * time.Second)

	for i := 0; i < 5; i++ {
		go func(workerIndex int) {
			for j := 0; j < 10000; j++ {
				message := "Low Priority Test Message " + strconv.Itoa(workerIndex) + "-" + strconv.Itoa(j)
				priority := 0
				sendMessageToBroker(message, priority)
			}
		}(i)
	}

	go func() {
		for i := 0; i < 1000; i++ {
			message := "High Priority Test Message " + strconv.Itoa(i)
			priority := 1
			sendMessageToBroker(message, priority)
		}
	}()

	fmt.Println("awaiting signal")
	<-done
	serverShutdownContext, shutdownTimeoutCancelFunc := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownTimeoutCancelFunc()
	server.Shutdown(serverShutdownContext)
	dispatcher.Stop()
	fmt.Println("exiting")
}

var client = &http.Client{Timeout: time.Second * 10}

func sendMessageToBroker(message string, priority int) {
	req, reqErr := http.NewRequest("POST", "http://localhost:58080/broker", strings.NewReader(message))
	if reqErr != nil {
		fmt.Println(reqErr)
		return
	}
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("X-Broker-Message-Priority", strconv.Itoa(priority))
	_, err := client.Do(req)
	if err != nil {
		fmt.Println(err)
	}
	defer req.Body.Close()
}
