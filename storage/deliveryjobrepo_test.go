package storage

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/imyousuf/webhook-broker/storage/data"
	"github.com/rs/xid"
	"github.com/stretchr/testify/assert"
)

var (
	consumers []*data.Consumer
)

const (
	consumerIDPrefix = "test-consumer-for-dj-"
)

func SetupForDeliveryJobTests() {
	testConsumers := 10
	consumerRepo := getConsumerRepo()
	consumers = make([]*data.Consumer, 0, testConsumers)
	for i := 0; i < testConsumers; i++ {
		consumer, _ := data.NewConsumer(channel1, consumerIDPrefix+strconv.Itoa(i), successfulGetTestToken, callbackURL)
		consumer.QuickFix()
		consumerRepo.Store(consumer)
		consumers = append(consumers, consumer)
	}
}

func getDeliverJobRepository() DeliveryJobRepository {
	return NewDeliveryJobRepository(testDB, getMessageRepository(), getConsumerRepo())
}

func getMessageForJob() *data.Message {
	message, _ := data.NewMessage(channel1, producer1, samplePayload, sampleContentType)
	return message
}

func getDeliveryJobsInFixture(message *data.Message) (jobs []*data.DeliveryJob) {
	jobs = make([]*data.DeliveryJob, 0, len(consumers))
	for _, consumer := range consumers {
		job, _ := data.NewDeliveryJob(message, consumer)
		jobs = append(jobs, job)
	}
	return jobs
}

func TestDispatchMessage(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		// FIXME: Split test into their own isolated test
		djRepo := getDeliverJobRepository()
		msgRepo := getMessageRepository()
		message := getMessageForJob()
		msgRepo.Create(message)
		jobs := getDeliveryJobsInFixture(message)
		err := djRepo.DispatchMessage(message, jobs...)
		assert.Nil(t, err)
		// Asserts for SetDispatched
		assert.Equal(t, data.MsgStatusDispatched, message.Status)
		assert.Greater(t, message.OutboxedAt.UnixNano(), message.ReceivedAt.UnixNano())
		assert.Greater(t, message.UpdatedAt.UnixNano(), message.CreatedAt.UnixNano())
		count := 0
		testDB.QueryRow("select count(*) from job where messageId like $1", message.ID).Scan(&count)
		assert.Equal(t, len(consumers), count)
		// Asserts for GetJobsForMessage
		dJobs, page, err := djRepo.GetJobsForMessage(message, data.NewPagination(nil, nil))
		assert.Nil(t, err)
		assert.Equal(t, len(consumers), len(dJobs))
		for _, dJob := range dJobs {
			assert.Equal(t, message, dJob.Message)
			assert.Contains(t, dJob.Listener.ConsumerID, consumerIDPrefix)
			assert.Equal(t, data.JobQueued, dJob.Status)
		}
		_, _, err = djRepo.GetJobsForMessage(message, page)
		assert.Equal(t, ErrPaginationDeadlock, err)
		// Asserts for conjunction pagination query append
		originalPage := *page
		page.Previous = nil
		dJobs, _, err = djRepo.GetJobsForMessage(message, page)
		assert.Nil(t, err)
		assert.Equal(t, 0, len(dJobs))
		page = &originalPage
		page.Next = nil
		dJobs, _, err = djRepo.GetJobsForMessage(message, page)
		assert.Nil(t, err)
		assert.Equal(t, 0, len(dJobs))
	})
	t.Run("MessageAlreadyDispatched", func(t *testing.T) {
		t.Parallel()
		djRepo := getDeliverJobRepository()
		msgRepo := getMessageRepository()
		message := getMessageForJob()
		msgRepo.Create(message)
		parallelMsg, _ := msgRepo.GetByID(message.ID.String())
		tx, _ := testDB.Begin()
		msgRepo.SetDispatched(context.WithValue(context.Background(), txContextKey, tx), parallelMsg)
		tx.Commit()
		jobs := getDeliveryJobsInFixture(message)
		err := djRepo.DispatchMessage(message, jobs...)
		assert.NotNil(t, err)
		assert.Equal(t, ErrNoRowsUpdated, err)
		count := -1
		testDB.QueryRow("select count(*) from job where messageId like $1", message.ID).Scan(&count)
		assert.Equal(t, 0, count)
	})
	t.Run("MsgNil", func(t *testing.T) {
		t.Parallel()
		djRepo := getDeliverJobRepository()
		message := getMessageForJob()
		jobs := getDeliveryJobsInFixture(message)
		assert.Equal(t, ErrInvalidStateToSave, djRepo.DispatchMessage(nil, jobs...))
	})
	t.Run("MsgInvalid", func(t *testing.T) {
		t.Parallel()
		djRepo := getDeliverJobRepository()
		message := getMessageForJob()
		jobs := getDeliveryJobsInFixture(message)
		message.ReceivedAt = time.Time{}
		assert.Equal(t, ErrInvalidStateToSave, djRepo.DispatchMessage(message, jobs...))
	})
	t.Run("AJobNil", func(t *testing.T) {
		t.Parallel()
		djRepo := getDeliverJobRepository()
		message := getMessageForJob()
		assert.Equal(t, ErrInvalidStateToSave, djRepo.DispatchMessage(message, nil))
	})
	t.Run("AJobInvalid", func(t *testing.T) {
		t.Parallel()
		djRepo := getDeliverJobRepository()
		message := getMessageForJob()
		jobs := getDeliveryJobsInFixture(message)
		jobs[0].DispatchReceivedAt = time.Time{}
		assert.Equal(t, ErrInvalidStateToSave, djRepo.DispatchMessage(message, jobs...))
	})
	t.Run("AJobWithDiffMsg", func(t *testing.T) {
		t.Parallel()
		djRepo := getDeliverJobRepository()
		message := getMessageForJob()
		jobs := getDeliveryJobsInFixture(message)
		newMsg := *message
		newMsg.ID = xid.New()
		jobs[0].Message = &newMsg
		assert.Equal(t, ErrInvalidStateToSave, djRepo.DispatchMessage(message, jobs...))
	})
}
