package controllers

import (
	"bytes"
	"context"
	"errors"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/google/wire"
	"github.com/gorilla/handlers"
	"github.com/imyousuf/webhook-broker/config"
	"github.com/imyousuf/webhook-broker/storage/data"
	"github.com/julienschmidt/httprouter"
)

// Controllers represents factory object containing all the controllers
type Controllers struct {
	StatusController    *StatusController
	ProducersController *ProducersController
	ProducerController  *ProducerController
	ChannelController   *ChannelController
	ConsumerController  *ConsumerController
	ConsumersController *ConsumersController
}

var (
	listener          ServerLifecycleListener
	routerInitializer sync.Once
	server            *http.Server
	// ControllerInjector for binding controllers
	ControllerInjector = wire.NewSet(ConfigureAPI, NewRouter, NewStatusController, NewProducersController, NewProducerController, NewChannelController, NewConsumerController, NewConsumersController, wire.Struct(new(Controllers), "StatusController", "ProducersController", "ProducerController", "ChannelController", "ConsumerController", "ConsumersController"))
	// ErrUnsupportedMediaType is returned when client does not provide appropriate `Content-Type` header
	ErrUnsupportedMediaType = errors.New("Media type not supported")
	// ErrConditionalFailed is returned when update is missing `If-Unmodified-Since` header
	ErrConditionalFailed = errors.New("Update failed due to mismatch of `If-Unmodified-Since` header value")
	// ErrNotFound is returned when resource is not found
	ErrNotFound = errors.New("Request resource not found")
	// ErrBadRequest is returned when protocol for a PUT/POST/DELETE request is not met
	ErrBadRequest = errors.New("Bad Request: Update is missing `If-Unmodified-Since` header ")
)

const (
	previousPaginationQueryParamKey = "previous"
	nextPaginationQueryParamKey     = "next"
	formDataContentTypeHeaderValue  = "application/x-www-form-urlencoded"
	headerContentType               = "Content-Type"
	headerUnmodifiedSince           = "If-Unmodified-Since"
	headerLastModified              = "Last-Modified"
	charset                         = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

// ServerLifecycleListener listens to key server lifecycle error
type ServerLifecycleListener interface {
	StartingServer()
	ServerStartFailed(err error)
	ServerShutdownCompleted()
}

// EndpointController represents very basic functionality of an endpoint
type EndpointController interface {
	GetPath() string
	FormatAsRelativeLink(params ...httprouter.Param) string
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
func NewRouter(controllers *Controllers) *httprouter.Router {
	apiRouter := httprouter.New()
	setupAPIRoutes(apiRouter, controllers.StatusController, controllers.ProducersController, controllers.ProducerController, controllers.ChannelController, controllers.ConsumerController, controllers.ConsumersController)
	return apiRouter
}

func getPagination(req *http.Request) *data.Pagination {
	result := &data.Pagination{}
	originalURL := req.URL
	previous := originalURL.Query().Get(previousPaginationQueryParamKey)
	if len(previous) > 0 {
		prevCursor := data.Cursor(previous)
		result.Previous = &prevCursor
	}
	next := originalURL.Query().Get(nextPaginationQueryParamKey)
	if len(next) > 0 {
		nextCursor := data.Cursor(next)
		result.Next = &nextCursor
	}
	return result
}

func getPaginationLinks(req *http.Request, pagination *data.Pagination) map[string]string {
	links := make(map[string]string)
	if pagination != nil {
		originalURL := req.URL
		if pagination.Previous != nil {
			previous := cloneBaseURL(originalURL)
			prevQueries := make(url.Values)
			prevQueries.Set(previousPaginationQueryParamKey, string(*pagination.Previous))
			previous.RawQuery = prevQueries.Encode()
			links[previousPaginationQueryParamKey] = previous.String()
		}
		if pagination.Next != nil {
			next := cloneBaseURL(originalURL)
			nextQueries := make(url.Values)
			nextQueries.Set(nextPaginationQueryParamKey, string(*pagination.Next))
			next.RawQuery = nextQueries.Encode()
			links[nextPaginationQueryParamKey] = next.String()
		}
	}
	return links
}

func cloneBaseURL(originalURL *url.URL) *url.URL {
	newURL := &url.URL{}
	newURL.Scheme = originalURL.Scheme
	newURL.Host = originalURL.Host
	newURL.Path = originalURL.Path
	newURL.RawPath = originalURL.RawPath
	return newURL
}

func setupAPIRoutes(apiRouter *httprouter.Router, endpoints ...EndpointController) {
	for _, endpoint := range endpoints {
		getEndpoint, ok := endpoint.(Get)
		if ok {
			apiRouter.GET(endpoint.GetPath(), getEndpoint.Get)
		}
		putEndpoint, ok := endpoint.(Put)
		if ok {
			apiRouter.PUT(endpoint.GetPath(), putEndpoint.Put)
		}
		postEndpoint, ok := endpoint.(Post)
		if ok {
			apiRouter.POST(endpoint.GetPath(), postEndpoint.Post)
		}
		deleteEndpoint, ok := endpoint.(Delete)
		if ok {
			apiRouter.DELETE(endpoint.GetPath(), deleteEndpoint.Delete)
		}
	}
}
func writeErr(w http.ResponseWriter, err error) {
	writeStatus(w, http.StatusInternalServerError, err)
}

func writeNotFound(w http.ResponseWriter) {
	writeStatus(w, http.StatusNotFound, ErrNotFound)
}

func writeBadRequest(w http.ResponseWriter) {
	writeStatus(w, http.StatusBadRequest, ErrBadRequest)
}

func writeUnsupportedMediaType(w http.ResponseWriter) {
	writeStatus(w, http.StatusUnsupportedMediaType, ErrUnsupportedMediaType)
}

func writePreconditionFailed(w http.ResponseWriter) {
	writeStatus(w, http.StatusPreconditionFailed, ErrUnsupportedMediaType)
}

func writeStatus(w http.ResponseWriter, code int, err error) {
	w.WriteHeader(code)
	w.Write([]byte(err.Error()))
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	// Write JSON
	var buf bytes.Buffer
	err := getJSON(&buf, data)
	if err != nil {
		// return error
		writeErr(w, err)
		return
	}
	w.WriteHeader(200)
	w.Header().Add("Content-Type", "application/json")
	w.Write(buf.Bytes())
}

var seededRand *rand.Rand = rand.New(
	rand.NewSource(time.Now().UnixNano()))

func randomToken() string {
	b := make([]byte, 12)
	for i := range b {
		b[i] = charset[seededRand.Intn(len(charset))]
	}
	return string(b)
}

func formatURL(params []httprouter.Param, urlTemplate string, urlParamNames ...string) (result string) {
	paramValues := make(map[string]string)
	for _, param := range params {
		for _, paramName := range urlParamNames {
			if param.Key == paramName {
				paramValues[paramName] = param.Value
			}
		}
	}
	result = urlTemplate
	for key, value := range paramValues {
		result = strings.ReplaceAll(result, ":"+key, value)
	}
	return result
}
