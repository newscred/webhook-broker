package controllers

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/google/wire"
	"github.com/gorilla/handlers"
	"github.com/imyousuf/webhook-broker/config"
	"github.com/julienschmidt/httprouter"
)

var (
	listener          ServerLifecycleListener
	routerInitializer sync.Once
	server            *http.Server
	// ControllerInjector for binding controllers
	ControllerInjector = wire.NewSet(ConfigureAPI, NewRouter, NewStatusController)
)

// ServerLifecycleListener listens to key server lifecycle error
type ServerLifecycleListener interface {
	StartingServer()
	ServerStartFailed(err error)
	ServerShutdownCompleted()
}

// Get represents GET Method Call to a resource
type Get interface {
	Get(w http.ResponseWriter, r *http.Request, ps httprouter.Params)
}

// Put represents PUT Method Call to a resource
type Put interface {
	Put(w http.ResponseWriter, r *http.Request, ps httprouter.Params)
}

// Post represents POST Method Call to a resource
type Post interface {
	Post(w http.ResponseWriter, r *http.Request, ps httprouter.Params)
}

// Delete represents DELETE Method Call to a resource
type Delete interface {
	Delete(w http.ResponseWriter, r *http.Request, ps httprouter.Params)
}

// RequestLogger is a simple io.Writer that allows requests to be logged
type RequestLogger struct {
}

func (rLogger RequestLogger) Write(p []byte) (n int, err error) {
	log.Println(string(p))
	return len(p), nil
}

// NotifyOnInterrupt registers channel to get notified when interrupt is captured
var NotifyOnInterrupt = func(stop *chan os.Signal) {
	signal.Notify(*stop, os.Interrupt)
}

// ConfigureAPI configures API Server with interrupt handling
func ConfigureAPI(httpConfig config.HTTPConfig, iListener ServerLifecycleListener, apiRouter *httprouter.Router) *http.Server {
	listener = iListener
	server = &http.Server{
		Handler:      handlers.LoggingHandler(RequestLogger{}, apiRouter),
		Addr:         httpConfig.GetHTTPListeningAddr(),
		ReadTimeout:  httpConfig.GetHTTPReadTimeout(),
		WriteTimeout: httpConfig.GetHTTPWriteTimeout(),
	}
	go func() {
		log.Println("Listening to http at -", httpConfig.GetHTTPListeningAddr())
		iListener.StartingServer()
		if serverListenErr := server.ListenAndServe(); serverListenErr != nil {
			iListener.ServerStartFailed(serverListenErr)
			log.Println(serverListenErr)
		}
	}()
	stop := make(chan os.Signal, 1)
	NotifyOnInterrupt(&stop)
	go func() {
		<-stop
		handleExit()
	}()
	return server
}

func handleExit() {
	log.Println("Shutting down the server...")
	serverShutdownContext, shutdownTimeoutCancelFunc := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownTimeoutCancelFunc()
	server.Shutdown(serverShutdownContext)
	log.Println("Server gracefully stopped!")
	listener.ServerShutdownCompleted()
}

// NewRouter returns a new instance of the router
func NewRouter(statusController *StatusController) *httprouter.Router {
	apiRouter := httprouter.New()
	setupAPIRoutes(apiRouter, statusController)
	return apiRouter
}

func setupAPIRoutes(apiRouter *httprouter.Router, statusController Get) {
	apiRouter.GET("/_status", statusController.Get)
}
