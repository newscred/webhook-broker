package controllers

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rs/xid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"
	"github.com/rs/zerolog/log"

	"net/http/pprof"

	"github.com/google/wire"
	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/config"
	"github.com/newscred/webhook-broker/storage/data"
)

var (
	listener          ServerLifecycleListener
	routerInitializer sync.Once
	server            *http.Server
	// ControllerInjector for binding controllers
	ControllerInjector = wire.NewSet(ConfigureAPI, NewRouter, NewStatusController, NewProducersController, NewProducerController, NewChannelController, NewChannelsController, NewConsumerController, NewConsumersController, NewJobsController, NewJobController, NewBroadcastController, NewMessageController, NewMessagesController, NewDLQController, wire.Struct(new(Controllers), "StatusController", "ProducersController", "ProducerController", "ChannelController", "ConsumerController", "ConsumersController", "JobsController", "JobController", "BroadcastController", "MessageController", "MessagesController", "DLQController", "ChannelsController"))
	// ErrUnsupportedMediaType is returned when client does not provide appropriate `Content-Type` header
	ErrUnsupportedMediaType = errors.New("Media type not supported")
	// ErrConditionalFailed is returned when update is missing `If-Unmodified-Since` header
	ErrConditionalFailed = errors.New("Update failed due to mismatch of `If-Unmodified-Since` header value")
	// ErrNotFound is returned when resource is not found
	ErrNotFound = errors.New("Request resource not found")
	// ErrBadRequest is returned when protocol for a PUT/POST/DELETE request is not met
	ErrBadRequest = errors.New("Bad Request: Update is missing `If-Unmodified-Since` header ")
	// ErrBadRequestForRequeue is returned when requeue form param does not match consumer token
	ErrBadRequestForRequeue = errors.New("`requeue` form param must match consumer token")
)

const (
	previousPaginationQueryParamKey = "previous"
	nextPaginationQueryParamKey     = "next"
	formDataContentTypeHeaderValue  = "application/x-www-form-urlencoded"
	headerContentType               = "Content-Type"
	headerUnmodifiedSince           = "If-Unmodified-Since"
	headerLastModified              = "Last-Modified"
	headerRequestID                 = "X-Request-ID"
	requestIDLogFieldKey            = "requestId"
	charset                         = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

type (
	// Controllers represents factory object containing all the controllers
	Controllers struct {
		StatusController    *StatusController
		ProducersController *ProducersController
		ProducerController  *ProducerController
		ChannelController   *ChannelController
		ChannelsController  *ChannelsController
		ConsumerController  *ConsumerController
		ConsumersController *ConsumersController
		JobsController      *JobsController
		JobController       *JobController
		BroadcastController *BroadcastController
		MessageController   *MessageController
		MessagesController  *MessagesController
		DLQController       *DLQController
	}

	// ServerLifecycleListener listens to key server lifecycle error
	ServerLifecycleListener interface {
		StartingServer()
		ServerStartFailed(err error)
		ServerShutdownCompleted()
	}

	// EndpointController represents very basic functionality of an endpoint
	EndpointController interface {
		GetPath() string
		FormatAsRelativeLink(params ...httprouter.Param) string
	}

	// Get represents GET Method Call to a resource
	Get interface {
		Get(w http.ResponseWriter, r *http.Request, ps httprouter.Params)
	}

	// Put represents PUT Method Call to a resource
	Put interface {
		Put(w http.ResponseWriter, r *http.Request, ps httprouter.Params)
	}

	// Post represents POST Method Call to a resource
	Post interface {
		Post(w http.ResponseWriter, r *http.Request, ps httprouter.Params)
	}

	// Delete represents DELETE Method Call to a resource
	Delete interface {
		Delete(w http.ResponseWriter, r *http.Request, ps httprouter.Params)
	}

	idKey struct{}
)

// NotifyOnInterrupt registers channel to get notified when interrupt is captured
var NotifyOnInterrupt = func(stop *chan os.Signal) {
	signal.Notify(*stop, os.Interrupt, os.Kill, syscall.SIGTERM)
}

func getRequestID(r *http.Request) (requestID string) {
	ctx := r.Context()
	requestID, ok := ctx.Value(idKey{}).(string)
	if !ok {
		requestID = r.Header.Get(headerRequestID)
		if len(requestID) < 1 {
			requestID = xid.New().String()
		}
		ctx = context.WithValue(ctx, idKey{}, requestID)
		r = r.WithContext(ctx)
	}
	return requestID
}

// getRequestIDHandler is similar to hlog.RequestIDHandler just the twist is it expects string as request id and not xid.ID
func getRequestIDHandler(fieldKey, headerName string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := getRequestID(r)
			ctx := r.Context()
			log := zerolog.Ctx(ctx)
			if len(fieldKey) > 0 {
				log.UpdateContext(func(c zerolog.Context) zerolog.Context {
					return c.Str(fieldKey, requestID)
				})
			}
			if len(headerName) > 0 {
				w.Header().Set(headerName, requestID)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func logAccess(r *http.Request, status, size int, duration time.Duration) {
	hlog.FromRequest(r).Info().
		Str("method", r.Method).
		Str("url", r.URL.String()).
		Int("status", status).
		Int("size", size).
		Dur("duration", duration).
		Msg("")
}

func getHandler(apiRouter *httprouter.Router) http.Handler {
	// Chain handlers - new handler to attach logger to request context, request id handler and lastly access log handler all ending with the our routes
	return hlog.NewHandler(log.Logger)(getRequestIDHandler(requestIDLogFieldKey, headerRequestID)(hlog.AccessHandler(logAccess)(apiRouter)))
}

// ConfigureAPI configures API Server with interrupt handling
func ConfigureAPI(httpConfig config.HTTPConfig, iListener ServerLifecycleListener, apiRouter *httprouter.Router) *http.Server {
	listener = iListener
	handler := getHandler(apiRouter)
	server = &http.Server{
		Handler:      handler,
		Addr:         httpConfig.GetHTTPListeningAddr(),
		ReadTimeout:  httpConfig.GetHTTPReadTimeout(),
		WriteTimeout: httpConfig.GetHTTPWriteTimeout(),
	}
	go func() {
		log.Print("Listening to http at -", httpConfig.GetHTTPListeningAddr())
		iListener.StartingServer()
		if serverListenErr := server.ListenAndServe(); serverListenErr != nil {
			iListener.ServerStartFailed(serverListenErr)
			log.Print(serverListenErr)
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
	log.Print("Shutting down the server...")
	serverShutdownContext, shutdownTimeoutCancelFunc := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownTimeoutCancelFunc()
	server.Shutdown(serverShutdownContext)
	log.Print("Server gracefully stopped!")
	listener.ServerShutdownCompleted()
}

// NewRouter returns a new instance of the router
func NewRouter(controllers *Controllers) *httprouter.Router {
	apiRouter := httprouter.New()
	apiRouter.HandlerFunc(http.MethodGet, "/debug/pprof/", pprof.Index)
	apiRouter.HandlerFunc(http.MethodGet, "/debug/pprof/cmdline", pprof.Cmdline)
	apiRouter.HandlerFunc(http.MethodGet, "/debug/pprof/profile", pprof.Profile)
	apiRouter.HandlerFunc(http.MethodGet, "/debug/pprof/symbol", pprof.Symbol)
	apiRouter.HandlerFunc(http.MethodGet, "/debug/pprof/trace", pprof.Trace)
	apiRouter.Handler(http.MethodGet, "/debug/pprof/goroutine", pprof.Handler("goroutine"))
	apiRouter.Handler(http.MethodGet, "/debug/pprof/mutex", pprof.Handler("mutex"))
	apiRouter.Handler(http.MethodGet, "/debug/pprof/heap", pprof.Handler("heap"))
	apiRouter.Handler(http.MethodGet, "/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
	apiRouter.Handler(http.MethodGet, "/debug/pprof/block", pprof.Handler("block"))
	setupAPIRoutes(apiRouter, controllers.StatusController, controllers.ProducersController, controllers.ProducerController, controllers.ChannelController,
		controllers.ConsumerController, controllers.ConsumersController, controllers.JobsController, controllers.JobController, controllers.BroadcastController, controllers.MessageController,
		controllers.MessagesController, controllers.DLQController, controllers.ChannelsController)
	return apiRouter
}

func getPagination(req *http.Request) *data.Pagination {
	result := &data.Pagination{}
	originalURL := req.URL
	previous := originalURL.Query().Get(previousPaginationQueryParamKey)
	if len(previous) > 0 {
		prevCursor, err := data.ParseCursor(previous)
		if err == nil {
			result.Previous = prevCursor
		}
	}
	next := originalURL.Query().Get(nextPaginationQueryParamKey)
	if len(next) > 0 {
		nextCursor, err := data.ParseCursor(next)
		if err == nil {
			result.Next = nextCursor
		}
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
			prevQueries.Set(previousPaginationQueryParamKey, pagination.Previous.String())
			previous.RawQuery = prevQueries.Encode()
			links[previousPaginationQueryParamKey] = previous.String()
		}
		if pagination.Next != nil {
			next := cloneBaseURL(originalURL)
			nextQueries := make(url.Values)
			nextQueries.Set(nextPaginationQueryParamKey, pagination.Next.String())
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
	if err != nil {
		w.Write([]byte(err.Error()))
	}
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
	for _, paramName := range urlParamNames {
		if val := findParam(params, paramName); len(val) > 0 {
			paramValues[paramName] = val
		}
	}
	result = urlTemplate
	for key, value := range paramValues {
		result = strings.ReplaceAll(result, ":"+key, value)
	}
	return result
}

func findParam(params httprouter.Params, name string) string {
	return params.ByName(name)
}
