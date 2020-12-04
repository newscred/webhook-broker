package controllers

import (
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/imyousuf/webhook-broker/storage"
	"github.com/imyousuf/webhook-broker/storage/data"
	"github.com/julienschmidt/httprouter"
)

const (
	producersPath                  = "/producers"
	producerIDPathParamKey         = "producerId"
	producerPath                   = "/producer/:" + producerIDPathParamKey
	formDataContentTypeHeaderValue = "application/x-www-form-urlencoded"
	headerContentType              = "Content-Type"
	headerUnmodifiedSince          = "If-Unmodified-Since"
	headerLastModified             = "Last-Modified"
)

// Producer is the
type Producer struct {
	ID        string
	Name      string
	Token     string
	ChangedAt time.Time
}

// ProducerController is for /producer/:prodId
type ProducerController struct {
	ProducerRepo storage.ProducerRepository
}

// Get implements the /producer/:prodId GET endpoint
func (prodController *ProducerController) Get(w http.ResponseWriter, r *http.Request, param httprouter.Params) {
	producerID := param.ByName(producerIDPathParamKey)
	producerModel, err := prodController.ProducerRepo.Get(producerID)
	if err != nil {
		writeNotFound(w)
		return
	}
	w.Header().Add(headerLastModified, producerModel.UpdatedAt.Format(http.TimeFormat))
	writeJSON(w, getProducer(producerModel))
}

func getProducer(producerModel *data.Producer) *Producer {
	x0 := &Producer{ID: producerModel.ProducerID, Name: producerModel.Name, Token: producerModel.Token, ChangedAt: producerModel.UpdatedAt}
	return x0
}

// Put implements the /producer/:prodId PUT endpoint
func (prodController *ProducerController) Put(w http.ResponseWriter, r *http.Request, param httprouter.Params) {
	validRequest := true
	if r.Header.Get(headerContentType) != formDataContentTypeHeaderValue {
		validRequest = false
		writeUnsupportedMediaType(w)
	}
	producerID := param.ByName(producerIDPathParamKey)
	producerModel, err := prodController.ProducerRepo.Get(producerID)
	if err == nil {
		unmodifiedSince := r.Header.Get(headerUnmodifiedSince)
		expectedHeader := producerModel.UpdatedAt.Format(http.TimeFormat)
		if len(unmodifiedSince) <= 0 {
			writeBadRequest(w)
			validRequest = false
		} else if unmodifiedSince != expectedHeader {
			writePreconditionFailed(w)
			validRequest = false
		}
	}
	if !validRequest {
		return
	}
	token := r.PostFormValue("token")
	if len(token) < 1 {
		token = randomToken()
	}
	name := r.PostFormValue("name")
	if len(name) < 1 {
		name = producerID
	}
	producer, _ := data.NewProducer(producerID, token)
	producer.Name = name
	producer, err = prodController.ProducerRepo.Store(producer)
	if err == nil {
		writeJSON(w, getProducer(producer))
	} else {
		writeErr(w, err)
	}
}

// GetPath returns the endpoint's path
func (prodController *ProducerController) GetPath() string {
	return producerPath
}

// FormatAsRelativeLink Format as relative URL of this resource based on the params
func (prodController *ProducerController) FormatAsRelativeLink(params ...httprouter.Param) string {
	for _, param := range params {
		if param.Key == producerIDPathParamKey {
			return strings.Replace(producerPath, ":"+producerIDPathParamKey, param.Value, -1)
		}
	}
	return producerPath
}

// Producers is the resource returned by /producers endpoint
type Producers struct {
	Producers []string
	Pages     map[string]string
}

// ProducersController for handling `/producers` endpoint
type ProducersController struct {
	ProducerRepo     storage.ProducerRepository
	ProducerEndpoint EndpointController
}

// Get implements the /producers endpoint
func (prodController *ProducersController) Get(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	producers, resultPagination, err := prodController.ProducerRepo.GetList(getPagination(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	producerURLs := make([]string, len(producers))
	for index, producer := range producers {
		producerURLs[index] = prodController.ProducerEndpoint.FormatAsRelativeLink(httprouter.Param{Key: producerIDPathParamKey, Value: producer.ProducerID})
	}
	data := Producers{Producers: producerURLs, Pages: getPaginationLinks(r, resultPagination)}
	writeJSON(w, data)
}

// GetPath returns the endpoint's path
func (prodController *ProducersController) GetPath() string {
	return producersPath
}

// FormatAsRelativeLink Format as relative URL of this resource based on the params
func (prodController *ProducersController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return producersPath
}

// NewProducerController initialize new producers controller
func NewProducerController(producerRepo storage.ProducerRepository) *ProducerController {
	return &ProducerController{ProducerRepo: producerRepo}
}

// NewProducersController initialize new producers controller
func NewProducersController(producerRepo storage.ProducerRepository, producerController *ProducerController) *ProducersController {
	return &ProducersController{ProducerRepo: producerRepo, ProducerEndpoint: producerController}
}

const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

var seededRand *rand.Rand = rand.New(
	rand.NewSource(time.Now().UnixNano()))

func randomToken() string {
	b := make([]byte, 12)
	for i := range b {
		b[i] = charset[seededRand.Intn(len(charset))]
	}
	return string(b)
}
