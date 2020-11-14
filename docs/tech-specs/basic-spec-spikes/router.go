package main

import (
	"net/http"
	"sync"

	"github.com/gorilla/mux"
)

var (
	router            *mux.Router
	routerInitializer sync.Once
)

func configureWebAPI() *http.Server {
	routerInitializer.Do(func() {
		router = mux.NewRouter()
		router.HandleFunc("/broker", brokerDispatch).Methods("POST").Name("broker")
		router.HandleFunc("/consumer", consume).Methods("POST").Name("consumer")
	})
	server := &http.Server{
		Handler: router,
		Addr:    ":58080",
	}
	return server
}
