package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
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

	jobQueue := InitializeJobQueue()

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

	for i := 0; i < 1000000; i++ {
		job := Job{Data: Message{Payload: "Test Message " + strconv.Itoa(i)}, Priority: 1}
		jobQueue <- job
	}
	fmt.Println("awaiting signal")
	<-done
	dispatcher.Stop()
	fmt.Println("exiting")
}
