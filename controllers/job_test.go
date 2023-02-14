package controllers

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
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
)

const (
	jobTestChannelID  = "job-test-channel-id"
	jobTestProducerID = "job-test-producer-id"
	jobTestConsumerID = "job-test-consumer-id"
)

// JobTestSetup is called from TestMain for the package
func JobTestSetup() {
	setupTestChannel()
	setupTestProducer()
	setupTestConsumer()

	deliveryJobRepo = storage.NewDeliveryJobRepository(db, messageRepo, consumerRepo)

	for index := 0; index < 50; index++ {
		indexString := strconv.Itoa(index)
		message, err := data.NewMessage(jobTestChannel, jobTestProducer, "payload "+indexString, "type")
		if err != nil {
			log.Fatal()
		}
		message.Priority = randomPriority()
		messageRepo.Create(message)
		job, err := data.NewDeliveryJob(message, jobTestConsumer)
		if err != nil {
			log.Fatal()
		}
		deliveryJobRepo.DispatchMessage(message, job)
	}
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

func randomPriority() uint {
	return uint(1 + rand.Intn(10))
}

func TestJobsFormatAsRelativeLink(t *testing.T) {
	t.Parallel()
	controller := NewJobsController(consumerRepo, deliveryJobRepo)
	assert.Equal(t, jobsPath, controller.GetPath())
	formattedPath := controller.FormatAsRelativeLink(
		httprouter.Param{Key: channelIDPathParamKey, Value: "someChannelId"},
		httprouter.Param{Key: consumerIDPathParamKey, Value: "someConsumerId"},
	)
	assert.Equal(t, "/channel/someChannelId/consumer/someConsumerId/queued-jobs", formattedPath)
}

func TestJobsControllerGet(t *testing.T) {
	t.Parallel()
	jobsController := NewJobsController(consumerRepo, deliveryJobRepo)
	testRouter := createTestRouter(jobsController)
	testURI := jobsController.FormatAsRelativeLink(
		httprouter.Param{Key: channelIDPathParamKey, Value: jobTestChannel.ChannelID},
		httprouter.Param{Key: consumerIDPathParamKey, Value: jobTestConsumer.ConsumerID},
	)
	t.Log(testURI)
	req, _ := http.NewRequest("GET", testURI, nil)
	rr := httptest.NewRecorder()
	testRouter.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	t.Log(body)
	jobListResult := &JobListResult{}
	json.NewDecoder(strings.NewReader(body)).Decode(jobListResult)

	jobs := jobListResult.Result
	assert.Equal(t, 25, len(jobs))

	assert.True(t, sort.SliceIsSorted(jobs, func(i, j int) bool {
		return jobs[i].Message.Priority > jobs[j].Message.Priority
	}))
}
