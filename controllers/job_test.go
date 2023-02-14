package controllers

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rs/zerolog/log"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/stretchr/testify/assert"
)

var (
	deliveryJobRepo storage.DeliveryJobRepository
	jobTestChannel  *data.Channel
	jobTestProducer *data.Producer
	jobTestConsumer *data.Consumer
	jobTestMessage  *data.Message
	postTestJob     *data.DeliveryJob
)

const (
	jobTestChannelID       = "job-test-channel-id"
	jobTestProducerID      = "job-test-producer-id"
	jobTestConsumerID      = "job-test-consumer-id"
	jobPostTestContentType = "application/json"
)

// JobTestSetup is called from TestMain for the package
func JobTestSetup() {
	setupTestChannel()
	setupTestProducer()
	setupTestConsumer()

	deliveryJobRepo = storage.NewDeliveryJobRepository(db, messageRepo, consumerRepo)

	var err error
	jobTestMessage, err = data.NewMessage(jobTestChannel, jobTestProducer, "payload", "type")
	if err != nil {
		log.Fatal()
	}
	messageRepo.Create(jobTestMessage)
	postTestJob, err = data.NewDeliveryJob(jobTestMessage, jobTestConsumer)
	if err != nil {
		log.Fatal()
	}
	deliveryJobRepo.DispatchMessage(jobTestMessage, postTestJob)
	deliveryJobRepo.MarkJobInflight(postTestJob)
	deliveryJobRepo.MarkJobDead(postTestJob)
}

func setupTestChannel() {
	channel, err := data.NewChannel(jobTestChannelID, successfulGetTestToken)
	if err != nil {
		log.Fatal()
	}
	jobTestChannel, err = channelRepo.Store(channel)
	if err != nil {
		log.Fatal().Err(err)
	}
}

func setupTestProducer() {
	producer, err := data.NewProducer(jobTestProducerID, successfulGetTestToken)
	if err != nil {
		log.Fatal()
	}
	jobTestProducer, err = producerRepo.Store(producer)
	if err != nil {
		log.Fatal().Err(err)
	}
}

func setupTestConsumer() {
	callbackURL, err := url.Parse("https://imytech.net/")
	if err != nil {
		log.Fatal().Err(err)
	}
	consumer, err := data.NewConsumer(jobTestChannel, jobTestConsumerID, successfulGetTestToken, callbackURL)
	if err != nil {
		log.Fatal()
	}
	jobTestConsumer, err = consumerRepo.Store(consumer)
	if err != nil {
		log.Fatal().Err(err)
	}
}

func TestJobFormatAsRelativeLink(t *testing.T) {
	t.Parallel()
	controller := NewJobController(channelRepo, consumerRepo, deliveryJobRepo)
	assert.Equal(t, jobPath, controller.GetPath())
	formattedPath := controller.FormatAsRelativeLink(
		httprouter.Param{Key: channelIDPathParamKey, Value: "someChannelId"},
		httprouter.Param{Key: consumerIDPathParamKey, Value: "someConsumerId"},
		httprouter.Param{Key: jobIDPathParamKey, Value: "someJobId"},
	)
	assert.Equal(t, "/channel/someChannelId/consumer/someConsumerId/job/someJobId", formattedPath)
}

func TestJobControllerPost(t *testing.T) {
	t.Parallel()
	jobsController := NewJobController(channelRepo, consumerRepo, deliveryJobRepo)
	testRouter := createTestRouter(jobsController)
	testURI := jobsController.FormatAsRelativeLink(
		httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
		httprouter.Param{Key: consumerIDPathParamKey, Value: jobTestConsumer.ConsumerID},
		httprouter.Param{Key: jobIDPathParamKey, Value: postTestJob.ID.String()},
	)
	t.Log(testURI)

	transitions := []data.JobStatus{data.JobQueued, data.JobInflight, data.JobDead, data.JobInflight, data.JobDelivered}
	for index := 1; index < len(transitions); index++ {
		previousState, nextState := transitions[index-1], transitions[index]

		testName := "Success:202-Accepted " + previousState.String() + " to " + nextState.String()
		t.Run(testName, func(t *testing.T) {
			bodyString := "{\"NextState\": \"" + nextState.String() + "\"}"
			requestBody := ioutil.NopCloser(strings.NewReader(bodyString))
			req, _ := http.NewRequest("POST", testURI, requestBody)

			req.Header.Add(headerContentType, jobPostTestContentType)
			req.Header.Add(headerChannelToken, jobTestChannel.Token)
			req.Header.Add(headerConsumerToken, jobTestConsumer.Token)

			rr := httptest.NewRecorder()
			testRouter.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusAccepted, rr.Code)

			job, _ := deliveryJobRepo.GetByID(postTestJob.ID.String())
			assert.Equal(t, job.Status, nextState)
		})

	}
}
