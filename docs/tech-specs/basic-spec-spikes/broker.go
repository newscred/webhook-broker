package main

import (
	"io/ioutil"
	"net/http"
	"strconv"
)

func brokerDispatch(w http.ResponseWriter, r *http.Request) {
	var jobQueue = InitializeJobQueue()
	body, err := ioutil.ReadAll(r.Body)
	defer r.Body.Close()
	if err == nil {
		priorityValue := r.Header.Get("X-Broker-Message-Priority")
		priority, pErr := strconv.Atoi(priorityValue)
		if pErr != nil {
			priority = 0
		}
		job := Job{Data: Message{Payload: string(body)}, Priority: priority}
		jobQueue <- job
	}
	w.WriteHeader(204)
}
