package controllers

import (
	"net/http"
	"time"

	"github.com/imyousuf/webhook-broker/storage"
	"github.com/imyousuf/webhook-broker/storage/data"
	"github.com/julienschmidt/httprouter"
)

const (
	producersPath          = "/producers"
	producerIDPathParamKey = "producerId"
	producerPath           = "/producer/:" + producerIDPathParamKey
)

// MsgStakeholder is the
type MsgStakeholder struct {
	ID        string
	Name      string
	Token     string
	ChangedAt time.Time
}

func getMessageStakeholder(id string, stakeholderModel *data.MessageStakeholder) *MsgStakeholder {
	return &MsgStakeholder{ID: id, Name: stakeholderModel.Name, Token: stakeholderModel.Token, ChangedAt: stakeholderModel.UpdatedAt}
}

// ListResult is the resource returned by /producers endpoint
type ListResult struct {
	Result []string
	Pages  map[string]string
	Links  map[string]string
}

func isConditionalUpdateCalled(w http.ResponseWriter, r *http.Request, channelModel *data.MessageStakeholder) bool {
	validRequest := true
	unmodifiedSince := r.Header.Get(headerUnmodifiedSince)
	expectedHeader := channelModel.UpdatedAt.Format(http.TimeFormat)
	if len(unmodifiedSince) <= 0 {
		writeBadRequest(w)
		validRequest = false
	} else if unmodifiedSince != expectedHeader {
		writePreconditionFailed(w)
		validRequest = false
	}
	return validRequest
}

func writeGetResult(err error, errFn func(w http.ResponseWriter), w http.ResponseWriter, msg *MsgStakeholder) {
	if err != nil {
		errFn(w)
	}
	w.Header().Add(headerLastModified, msg.ChangedAt.Format(http.TimeFormat))
	writeJSON(w, msg)
}

func getUpdateData(r *http.Request, defaultName string) (token string, name string) {
	token = r.PostFormValue("token")
	if len(token) < 1 {
		token = randomToken()
	}
	name = r.PostFormValue("name")
	if len(name) < 1 {
		name = defaultName
	}
	return token, name
}

// ProducerController is for /producer/:prodId
type ProducerController struct {
	ProducerRepo storage.ProducerRepository
}

// Get implements the /producer/:prodId GET endpoint
func (prodController *ProducerController) Get(w http.ResponseWriter, r *http.Request, param httprouter.Params) {
	producerID := param.ByName(producerIDPathParamKey)
	producerModel, err := prodController.ProducerRepo.Get(producerID)
	writeGetResult(err, writeNotFound, w, getMessageStakeholder(producerID, &producerModel.MessageStakeholder))
}

// Put implements the /producer/:prodId PUT endpoint
func (prodController *ProducerController) Put(w http.ResponseWriter, r *http.Request, param httprouter.Params) {
	validRequest := checkFormContentType(r, w)
	producerID := param.ByName(producerIDPathParamKey)
	producerModel, err := prodController.ProducerRepo.Get(producerID)
	if err == nil && validRequest {
		validRequest = isConditionalUpdateCalled(w, r, &producerModel.MessageStakeholder)
	}
	if !validRequest {
		return
	}
	token, name := getUpdateData(r, producerID)
	producer, _ := data.NewProducer(producerID, token)
	producer.Name = name
	producer, err = prodController.ProducerRepo.Store(producer)
	writeGetResult(err, func(w http.ResponseWriter) { writeErr(w, err) }, w, getMessageStakeholder(producerID, &producer.MessageStakeholder))
}

func checkFormContentType(r *http.Request, w http.ResponseWriter) bool {
	validRequest := true
	if r.Header.Get(headerContentType) != formDataContentTypeHeaderValue {
		validRequest = false
		writeUnsupportedMediaType(w)
	}
	return validRequest
}

// GetPath returns the endpoint's path
func (prodController *ProducerController) GetPath() string {
	return producerPath
}

// FormatAsRelativeLink Format as relative URL of this resource based on the params
func (prodController *ProducerController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return formatURL(params, producerPath, producerIDPathParamKey)
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
	data := ListResult{Result: producerURLs, Pages: getPaginationLinks(r, resultPagination)}
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
