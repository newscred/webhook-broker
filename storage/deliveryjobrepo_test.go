package storage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/rs/xid"
	"github.com/stretchr/testify/assert"
)

var (
	consumers []*data.Consumer
)

const (
	messagePriority = 5
)

func SetupForDeliveryJobTests() {
	consumerRepo := NewConsumerRepository(testDB, NewChannelRepository(testDB))
	consumers = SetupForDeliveryJobTestsWithOptions(&DeliveryJobSetupOptions{ConsumerRepo: consumerRepo, ConsumerChannel: channel1})
}

func getDeliverJobRepository() DeliveryJobRepository {
	return NewDeliveryJobRepository(testDB, getMessageRepository(), getConsumerRepo())
}

func getMessageForJob() *data.Message {
	message, _ := data.NewMessage(channel1, producer1, samplePayload, sampleContentType, data.HeadersMap{})
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

func dispatchJobs(djRepo DeliveryJobRepository, message *data.Message, jobs []*data.DeliveryJob) error {
	err := djRepo.DispatchMessage(message, jobs...)
	if err != nil {
		log.Error().Err(err).Msg("Error dispatching message")
		return err
	}
	return nil
}

func markJobDelivered(djRepo DeliveryJobRepository, job *data.DeliveryJob) error {
	err := djRepo.MarkJobInflight(job)
	if err != nil {
		log.Error().Err(err).Msg("Error marking job inflight")
		return err
	}
	err = djRepo.MarkJobDelivered(job)
	if err != nil {
		log.Error().Err(err).Msg("Error marking job delivered")
		return err
	}
	return nil
}

func TestDispatchMessage(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		// FIXME: Split test into their own isolated test
		djRepo := getDeliverJobRepository()
		msgRepo := getMessageRepository()
		message := getMessageForJob()
		message.Priority = messagePriority
		msgRepo.Create(message)
		jobs := getDeliveryJobsInFixture(message)
		err := djRepo.DispatchMessage(message, jobs...)
		assert.Nil(t, err)
		// Asserts for SetDispatched
		assert.Equal(t, data.MsgStatusDispatched, message.Status)
		assert.Greater(t, message.OutboxedAt.UnixNano(), message.ReceivedAt.UnixNano())
		assert.Greater(t, message.UpdatedAt.UnixNano(), message.CreatedAt.UnixNano())
		count := 0
		testDB.QueryRow("select count(*) from job where messageId like ?", message.ID).Scan(&count)
		assert.Equal(t, len(consumers), count)
		// Asserts for GetJobsForMessage
		dJobs, page, err := djRepo.GetJobsForMessage(message, data.NewPagination(nil, nil))
		assert.Nil(t, err)
		assert.Equal(t, len(consumers), len(dJobs))
		for _, dJob := range dJobs {
			assert.Equal(t, message, dJob.Message)
			assert.Contains(t, dJob.Listener.ConsumerID, consumerIDPrefix)
			assert.Equal(t, data.JobQueued, dJob.Status)
			assert.Equal(t, message.Priority, dJob.Priority)
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
	t.Run("NoJobs", func(t *testing.T) {
		t.Parallel()
		djRepo := getDeliverJobRepository()
		msgRepo := getMessageRepository()
		message := getMessageForJob()
		msgRepo.Create(message)
		err := djRepo.DispatchMessage(message)
		assert.Nil(t, err)
		// Asserts for SetDispatched
		assert.Equal(t, data.MsgStatusDispatched, message.Status)
		assert.Greater(t, message.OutboxedAt.UnixNano(), message.ReceivedAt.UnixNano())
		assert.Greater(t, message.UpdatedAt.UnixNano(), message.CreatedAt.UnixNano())
		count := 0
		testDB.QueryRow("select count(*) from job where messageId like ?", message.ID).Scan(&count)
		assert.Equal(t, 0, count)
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
		testDB.QueryRow("select count(*) from job where messageId like ?", message.ID).Scan(&count)
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

func TestGetJobsForMessages(t *testing.T) {
	// Isolated channel/consumers so this test's extra jobs don't perturb the exact
	// package-wide counts asserted by TestGetJobStatusCountsGroupedByConsumer.
	const batchConsumerIDPrefix = "batch-jobs-consumer-"
	const batchConsumerCount = 3
	djRepo := getDeliverJobRepository()
	msgRepo := getMessageRepository()
	channelRepo := NewChannelRepository(testDB)
	batchChannel, err := data.NewChannel("batch-jobs-test-channel", successfulGetTestToken)
	assert.Nil(t, err)
	batchChannel, err = channelRepo.Store(batchChannel)
	assert.Nil(t, err)
	batchConsumers := SetupForDeliveryJobTestsWithOptions(&DeliveryJobSetupOptions{
		ConsumerCount:    batchConsumerCount,
		ConsumerIDPrefix: batchConsumerIDPrefix,
		ConsumerRepo:     getConsumerRepo(),
		ConsumerChannel:  batchChannel,
	})
	// Remove every message/job this test creates so the package-wide job counts other
	// tests assert on (TestGetJobStatusCountsGroupedByConsumer) are left untouched.
	createdMessages := make([]*data.Message, 0)
	t.Cleanup(func() {
		for _, message := range createdMessages {
			assert.Nil(t, djRepo.DeleteJobsForMessage(message))
			assert.Nil(t, msgRepo.DeleteMessage(message))
		}
	})
	dispatchMessageWithJobs := func() *data.Message {
		message, msgErr := data.NewMessage(batchChannel, producer1, samplePayload, sampleContentType, data.HeadersMap{})
		assert.Nil(t, msgErr)
		assert.Nil(t, msgRepo.Create(message))
		jobs := make([]*data.DeliveryJob, 0, len(batchConsumers))
		for _, consumer := range batchConsumers {
			job, _ := data.NewDeliveryJob(message, consumer)
			jobs = append(jobs, job)
		}
		assert.Nil(t, dispatchJobs(djRepo, message, jobs))
		createdMessages = append(createdMessages, message)
		return message
	}
	assertGrouped := func(t *testing.T, jobsByMessage map[string][]*data.DeliveryJob, message *data.Message) {
		jobs, ok := jobsByMessage[message.ID.String()]
		assert.True(t, ok)
		assert.Equal(t, batchConsumerCount, len(jobs))
		for _, job := range jobs {
			assert.Equal(t, message.ID, job.Message.ID)
			assert.NotNil(t, job.Listener)
			assert.Contains(t, job.Listener.ConsumerID, batchConsumerIDPrefix)
		}
	}
	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		jobsByMessage, err := djRepo.GetJobsForMessages([]string{})
		assert.Nil(t, err)
		assert.Equal(t, 0, len(jobsByMessage))
	})
	t.Run("UnknownIDs", func(t *testing.T) {
		t.Parallel()
		jobsByMessage, err := djRepo.GetJobsForMessages([]string{xid.New().String(), xid.New().String()})
		assert.Nil(t, err)
		assert.Equal(t, 0, len(jobsByMessage))
	})
	t.Run("GroupsByMessage", func(t *testing.T) {
		message1 := dispatchMessageWithJobs()
		message2 := dispatchMessageWithJobs()
		jobsByMessage, err := djRepo.GetJobsForMessages([]string{message1.ID.String(), message2.ID.String(), xid.New().String()})
		assert.Nil(t, err)
		assert.Equal(t, 2, len(jobsByMessage))
		assertGrouped(t, jobsByMessage, message1)
		assertGrouped(t, jobsByMessage, message2)
	})
	t.Run("SpansChunkBoundary", func(t *testing.T) {
		// Place two real messages on opposite sides of the getJobsForMessagesChunkSize
		// boundary so both chunk iterations are exercised; padding ids resolve to no rows.
		message1 := dispatchMessageWithJobs()
		message2 := dispatchMessageWithJobs()
		messageIDs := make([]string, 0, getJobsForMessagesChunkSize+2)
		messageIDs = append(messageIDs, message1.ID.String())
		for len(messageIDs) < getJobsForMessagesChunkSize {
			messageIDs = append(messageIDs, xid.New().String())
		}
		messageIDs = append(messageIDs, message2.ID.String())
		assert.Greater(t, len(messageIDs), getJobsForMessagesChunkSize)
		jobsByMessage, err := djRepo.GetJobsForMessages(messageIDs)
		assert.Nil(t, err)
		assert.Equal(t, 2, len(jobsByMessage))
		assertGrouped(t, jobsByMessage, message1)
		assertGrouped(t, jobsByMessage, message2)
	})
}

func TestStatusUpdatesForJob(t *testing.T) {
	djRepo := getDeliverJobRepository()
	msgRepo := getMessageRepository()
	message := getMessageForJob()
	msgRepo.Create(message)
	jobs := getDeliveryJobsInFixture(message)
	err := djRepo.DispatchMessage(message, jobs...)
	assert.Nil(t, err)
	t.Run("GetByID", func(t *testing.T) {
		t.Parallel()
		iJob := jobs[0]
		dJob, err := djRepo.GetByID(iJob.ID.String())
		assert.Nil(t, err)
		assert.Equal(t, data.JobQueued, dJob.Status)
		_, err = djRepo.GetByID("random-does-not-exist")
		assert.NotNil(t, err)
		assert.Equal(t, sql.ErrNoRows, err)
	})
	t.Run("MarkJobInflight", func(t *testing.T) {
		t.Parallel()
		job := jobs[1]
		err := djRepo.MarkJobInflight(job)
		assert.Nil(t, err)
		err = djRepo.MarkJobInflight(job)
		assert.NotNil(t, err)
		dJob, err := djRepo.GetByID(job.ID.String())
		assert.Equal(t, data.JobInflight, dJob.Status)
	})
	t.Run("MarkJobDead", func(t *testing.T) {
		t.Parallel()
		job := jobs[2]
		err = djRepo.MarkJobDead(job)
		assert.NotNil(t, err)
		err := djRepo.MarkJobInflight(job)
		assert.Nil(t, err)
		err = djRepo.MarkJobDead(job)
		assert.Nil(t, err)
		err = djRepo.MarkJobDead(job)
		assert.NotNil(t, err)
		dJob, err := djRepo.GetByID(job.ID.String())
		assert.Equal(t, data.JobDead, dJob.Status)
	})
	t.Run("MarkJobDelivered", func(t *testing.T) {
		t.Parallel()
		job := jobs[3]
		err = djRepo.MarkJobDelivered(job)
		assert.NotNil(t, err)
		err := djRepo.MarkJobInflight(job)
		assert.Nil(t, err)
		err = djRepo.MarkJobDelivered(job)
		assert.Nil(t, err)
		err = djRepo.MarkJobDelivered(job)
		assert.NotNil(t, err)
		dJob, err := djRepo.GetByID(job.ID.String())
		assert.Equal(t, data.JobDelivered, dJob.Status)
	})
	t.Run("MarkJobRetry", func(t *testing.T) {
		t.Parallel()
		now := time.Now()
		next := 10 * time.Minute
		job := jobs[4]
		err = djRepo.MarkJobRetry(job, next)
		assert.NotNil(t, err)
		err := djRepo.MarkJobInflight(job)
		assert.Nil(t, err)
		err = djRepo.MarkJobRetry(job, next)
		assert.Nil(t, err)
		err = djRepo.MarkJobRetry(job, next)
		assert.NotNil(t, err)
		dJob, err := djRepo.GetByID(job.ID.String())
		assert.Equal(t, data.JobQueued, dJob.Status)
		assert.Greater(t, dJob.EarliestNextAttemptAt.UnixNano(), now.UnixNano())
	})
	t.Run("MarkDeadJobAsInflight", func(t *testing.T) {
		t.Parallel()
		job := jobs[5]
		err := djRepo.MarkDeadJobAsInflight(job)
		assert.Error(t, err)
		err = djRepo.MarkJobInflight(job)
		assert.NoError(t, err)
		err = djRepo.MarkDeadJobAsInflight(job)
		assert.Error(t, err)
		err = djRepo.MarkJobDead(job)
		assert.NoError(t, err)
		currentRetryAttempCount := job.RetryAttemptCount
		err = djRepo.MarkDeadJobAsInflight(job)
		assert.NoError(t, err)
		dJob, err := djRepo.GetByID(job.ID.String())
		assert.NoError(t, err)
		assert.Equal(t, data.JobInflight, dJob.Status)
		assert.Equal(t, currentRetryAttempCount+1, dJob.RetryAttemptCount)
	})
}

func TestStatusBasedJobsListing(t *testing.T) {
	t.Run("RetryListQueryError", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		defer func() { log.Logger = oldLogger }()
		errString := "sample select error"
		expectedErr := errors.New(errString)
		db, mock, _ := sqlmock.New()
		djRepo := &DeliveryJobDBRepository{db: db}
		mock.ExpectQuery(jobCommonSelectQuery).WillReturnError(expectedErr)
		mock.MatchExpectationsInOrder(true)
		jobs := djRepo.GetJobsInflightSince(configuration.RationalDelay)
		assert.Equal(t, 0, len(jobs))
		assert.Contains(t, buf.String(), errString)

	})
	pullConsumer, err := data.NewConsumer(channel1, "test-pull-consumer", "token", callbackURL, data.PullConsumerStr)
	assert.Nil(t, err)
	_, err = getConsumerRepo().Store(pullConsumer)
	assert.Nil(t, err)
	djRepo := getDeliverJobRepository()
	msgRepo := getMessageRepository()
	message := getMessageForJob()
	msgRepo.Create(message)
	jobs := getDeliveryJobsInFixture(message)
	err = djRepo.DispatchMessage(message, jobs...)
	inflightJob := jobs[0]
	for _, job := range jobs {
		if job.Listener.Type != data.PullConsumer {
			inflightJob = job
			break
		}
	}
	djRepo.MarkJobInflight(inflightJob)
	assert.Nil(t, err)
	time.Sleep(configuration.RationalDelay + 1)
	t.Run("SuccessInflightRecoveryList", func(t *testing.T) {
		thisJobs := djRepo.GetJobsInflightSince(configuration.RationalDelay)
		assert.LessOrEqual(t, 1, len(thisJobs))
		found := false
		for _, job := range thisJobs {
			if job.ID == inflightJob.ID {
				found = true
			}
		}
		assert.True(t, found)
	})
	t.Run("SuccessRetryList", func(t *testing.T) {
		thisJobs := djRepo.GetJobsReadyForInflightSince(configuration.RationalDelay, 4)
		assert.LessOrEqual(t, len(jobs)-1, len(thisJobs))
		found := false
		for _, thisJob := range thisJobs {
			assert.True(t, thisJob.Listener.Type == data.PushConsumer)
		}
		for index := 1; index < len(jobs); index++ {
			for _, job := range thisJobs {
				if job.ID == jobs[index].ID {
					found = true
				}
			}
			assert.True(t, found)
		}
	})
	t.Run("RetryListOrderedByEarliestNextAttemptAt", func(t *testing.T) {
		thisJobs := djRepo.GetJobsReadyForInflightSince(configuration.RationalDelay, 4)
		assert.True(t, sort.SliceIsSorted(thisJobs, func(i, j int) bool {
			return thisJobs[i].EarliestNextAttemptAt.Before(thisJobs[j].EarliestNextAttemptAt)
		}), "retry sweep must hand back oldest-due-first so long waiting jobs are not starved")
	})
	t.Run("RetryListQueryError", func(t *testing.T) {
		var buf bytes.Buffer
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		defer func() { log.Logger = oldLogger }()
		errString := "sample retry select error"
		db, mock, _ := sqlmock.New()
		errRepo := &DeliveryJobDBRepository{db: db}
		mock.ExpectQuery("FROM job WHERE status").WillReturnError(errors.New(errString))
		mock.MatchExpectationsInOrder(true)
		thisJobs := errRepo.GetJobsReadyForInflightSince(configuration.RationalDelay, 4)
		assert.Equal(t, 0, len(thisJobs))
		assert.Contains(t, buf.String(), errString)
	})
}

func TestGetJobsReadyForInflightSincePaginates(t *testing.T) {
	djRepo := getDeliverJobRepository()
	msgRepo := getMessageRepository()
	pageOverflowChannel, err := data.NewChannel("channel-for-retry-paging", "sampletoken")
	assert.Nil(t, err)
	pageOverflowChannel.QuickFix()
	pageOverflowChannel, err = getChannelRepo().Store(pageOverflowChannel)
	assert.Nil(t, err)
	jobCount := readyForInflightJobsPageSize + 20
	pageConsumers := SetupForDeliveryJobTestsWithOptions(&DeliveryJobSetupOptions{IgnoreSettingConsumers: true,
		ConsumerCount: jobCount, ConsumerIDPrefix: "retry-paging-consumer-", ConsumerChannel: pageOverflowChannel,
		ConsumerRepo: getConsumerRepo()})
	assert.Equal(t, jobCount, len(pageConsumers))

	message := getMessageForJob()
	assert.Nil(t, msgRepo.Create(message))
	pageJobs := make([]*data.DeliveryJob, 0, jobCount)
	for _, consumer := range pageConsumers {
		job, _ := data.NewDeliveryJob(message, consumer)
		pageJobs = append(pageJobs, job)
	}
	assert.Nil(t, djRepo.DispatchMessage(message, pageJobs...))
	defer func() { assert.Nil(t, djRepo.DeleteJobsForMessage(message)) }()

	// Spread earliestNextAttemptAt so the rows straddle pages instead of colliding on one timestamp.
	pastTime := time.Now().Add(-1 * time.Hour)
	for index, job := range pageJobs {
		_, err := testDB.Exec("UPDATE job SET earliestNextAttemptAt = ? WHERE id like ?",
			pastTime.Add(time.Duration(index)*time.Second), job.ID)
		assert.Nil(t, err)
	}

	foundJobs := djRepo.GetJobsReadyForInflightSince(configuration.RationalDelay, 4)
	assert.Less(t, readyForInflightJobsPageSize, len(foundJobs), "fixture must overflow a single page")
	seen := make(map[xid.ID]int)
	for _, job := range foundJobs {
		seen[job.ID] = seen[job.ID] + 1
	}
	for _, job := range pageJobs {
		assert.Equal(t, 1, seen[job.ID], "job "+job.ID.String()+" must be returned exactly once across pages")
	}
}

// Regression guard: the ACTUAL queries used by GetJobsReadyForInflightSince must be served by
// retry_job with no filesort, else the sweep degrades to ~190k rows per LIMIT 100 page. Binds to
// the real query consts, so reverting the ORDER BY or the row-value cursor fails the test. Runs
// on SQLite, which picks retry_job either way but reports the sort as "USE TEMP B-TREE FOR ORDER
// BY" -- so that assertion, not the index name, is the one that catches a regression here.
func TestReadyForInflightJobsQueryPlan(t *testing.T) {
	explain := func(t *testing.T, query string, args ...interface{}) string {
		rows, err := testDB.Query("EXPLAIN QUERY PLAN "+query, args...)
		assert.NoError(t, err)
		defer rows.Close()
		var plan strings.Builder
		for rows.Next() {
			var id, parent, notused int
			var detail string
			assert.NoError(t, rows.Scan(&id, &parent, &notused, &detail))
			plan.WriteString(detail)
			plan.WriteString("\n")
		}
		assert.NoError(t, rows.Err())
		return plan.String()
	}
	t.Run("FirstPage", func(t *testing.T) {
		plan := explain(t, readyForInflightJobsFirstPageQuery, data.JobQueued, time.Now(), 4, data.PullConsumer)
		assert.Contains(t, plan, "retry_job", "query should use the retry index; plan was:\n"+plan)
		assert.NotContains(t, plan, "USE TEMP B-TREE FOR ORDER BY", "query should not filesort; plan was:\n"+plan)
	})
	t.Run("NextPage", func(t *testing.T) {
		plan := explain(t, readyForInflightJobsNextPageQuery, data.JobQueued, time.Now(), 4, data.PullConsumer,
			time.Now().Add(-1*time.Hour), xid.New().String())
		assert.Contains(t, plan, "retry_job", "cursor query should use the retry index; plan was:\n"+plan)
		assert.NotContains(t, plan, "USE TEMP B-TREE FOR ORDER BY", "cursor query should not filesort; plan was:\n"+plan)
	})
}

func TestGetJobsForConsumer(t *testing.T) {
	djRepo := getDeliverJobRepository()
	msgRepo := getMessageRepository()

	message2 := getMessageForJob()
	message2.Headers["x-count"] = "7"
	msgRepo.Create(message2)
	jobs2 := getDeliveryJobsInFixture(message2)
	err := djRepo.DispatchMessage(message2, jobs2...)
	assert.Nil(t, err)

	message := getMessageForJob()
	msgRepo.Create(message)
	jobs := getDeliveryJobsInFixture(message)
	err = djRepo.DispatchMessage(message, jobs...)
	testJob := jobs[5]
	djRepo.MarkJobInflight(testJob)
	assert.Nil(t, err)
	t.Run("PaginationDeadlock", func(t *testing.T) {
		t.Parallel()
		_, _, err := djRepo.GetJobsForConsumer(testJob.Listener, data.JobInflight, data.NewPagination(testJob, testJob))
		assert.Equal(t, ErrPaginationDeadlock, err)
	})
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		rJobs, page, err := djRepo.GetJobsForConsumer(testJob.Listener, data.JobInflight, data.NewPagination(nil, nil))
		assert.Nil(t, err)
		assert.LessOrEqual(t, 1, len(rJobs))
		assert.NotNil(t, page.Next)
		assert.NotNil(t, page.Previous)
		found := false
		for _, job := range rJobs {
			if job.ID == testJob.ID {
				found = true
			}
			assert.Equal(t, job.Listener.ID, testJob.Listener.ID)
			assert.Equal(t, data.JobInflight, job.Status)
			if job.Message.ID == message2.ID {
				assert.Equal(t, job.Message.Headers["x-count"], message2.Headers["x-count"])
			} else {
				assert.Equal(t, job.Message.Headers["x-count"], "")
			}
		}
		assert.True(t, found)
		rJobs, page2, err := djRepo.GetJobsForConsumer(testJob.Listener, data.JobInflight, &data.Pagination{Previous: page.Previous})
		assert.Equal(t, 0, len(rJobs))
		assert.Nil(t, page2.Next)
		assert.Nil(t, page2.Previous)
		rJobs, page3, err := djRepo.GetJobsForConsumer(testJob.Listener, data.JobInflight, &data.Pagination{Next: page.Next})
		assert.Equal(t, 0, len(rJobs))
		assert.Nil(t, page3.Next)
		assert.Nil(t, page3.Previous)
	})
}

func TestGetPrioritizedJobsForConsumer(t *testing.T) {
	djRepo := getDeliverJobRepository()
	msgRepo := getMessageRepository()

	message := getMessageForJob()
	msgRepo.Create(message)
	jobs := getDeliveryJobsInFixture(message)
	err := djRepo.DispatchMessage(message, jobs...)
	assert.NoError(t, err)

	message2 := getMessageForJob()
	msgRepo.Create(message2)
	jobs2 := getDeliveryJobsInFixture(message2)
	err = djRepo.DispatchMessage(message2, jobs2...)
	assert.NoError(t, err)

	testJob := jobs[5]
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		pageSize := 5
		rJobs, err := djRepo.GetPrioritizedJobsForConsumer(testJob.Listener, data.JobQueued, pageSize)
		assert.NoError(t, err)
		assert.LessOrEqual(t, 1, len(rJobs))
		assert.GreaterOrEqual(t, pageSize, len(rJobs))
		found := false
		for _, job := range rJobs {
			if job.ID == testJob.ID {
				found = true
			}
			assert.Equal(t, job.Listener.ID, testJob.Listener.ID)
			assert.Equal(t, data.JobQueued, job.Status)
		}
		assert.True(t, found)
		assert.True(t, sort.SliceIsSorted(rJobs, func(i, j int) bool {
			return rJobs[i].Message.Priority > rJobs[j].Message.Priority
		}))
	})
	// Regression guard: the ACTUAL query used by GetPrioritizedJobsForConsumer
	// (prioritizedJobsForConsumerQuery) must be served by the job_consumer_status_priority
	// index (migration 000013) with no filesort. Without it the DB filesorts the whole QUEUED
	// backlog -> O(backlog) latency -> 504 for busy consumers. This binds to the real query
	// const, so reverting `=` back to `like` (which filesorts) fails the test. Runs against the
	// SQLite test DB; SQLite reports a filesort as "USE TEMP B-TREE FOR ORDER BY".
	t.Run("UsesPrioritizedIndexNoFilesort", func(t *testing.T) {
		query := "EXPLAIN QUERY PLAN " + prioritizedJobsForConsumerQuery
		rows, err := testDB.Query(query, testJob.Listener.ID.String(), data.JobQueued, 25)
		assert.NoError(t, err)
		defer rows.Close()
		var plan strings.Builder
		for rows.Next() {
			var id, parent, notused int
			var detail string
			assert.NoError(t, rows.Scan(&id, &parent, &notused, &detail))
			plan.WriteString(detail)
			plan.WriteString("\n")
		}
		assert.NoError(t, rows.Err())
		planStr := plan.String()
		assert.Contains(t, planStr, "job_consumer_status_priority", "query should use the prioritized index; plan was:\n"+planStr)
		assert.NotContains(t, planStr, "USE TEMP B-TREE FOR ORDER BY", "query should not filesort; plan was:\n"+planStr)
	})
}

func TestRequeueDeadJobsForConsumer(t *testing.T) {
	djRepo := getDeliverJobRepository()
	msgRepo := getMessageRepository()

	message2 := getMessageForJob()
	msgRepo.Create(message2)
	jobs2 := getDeliveryJobsInFixture(message2)
	err := djRepo.DispatchMessage(message2, jobs2...)
	assert.Nil(t, err)

	message := getMessageForJob()
	msgRepo.Create(message)
	jobs := getDeliveryJobsInFixture(message)
	err = djRepo.DispatchMessage(message, jobs...)
	testJobs := []*data.DeliveryJob{jobs[5], jobs2[5]}

	t.Run("RequeueDeadJob", func(t *testing.T) {
		sampleTestJob := jobs[5]
		err := djRepo.MarkJobInflight(sampleTestJob)
		assert.Nil(t, err)
		err = djRepo.MarkJobDead(sampleTestJob)
		assert.Nil(t, err)
		_, err = djRepo.RequeueDeadJob(sampleTestJob)
		assert.Nil(t, err)
		_, err = djRepo.RequeueDeadJob(sampleTestJob)
		assert.NotNil(t, err)
	})

	for _, testJob := range testJobs {
		err := djRepo.MarkJobInflight(testJob)
		assert.Nil(t, err)
		err = djRepo.MarkJobDead(testJob)
		assert.Nil(t, err)
	}
	rJobs, _, err := djRepo.GetJobsForConsumer(testJobs[0].Listener, data.JobDead, data.NewPagination(nil, nil))
	assert.Nil(t, err)
	assert.LessOrEqual(t, 2, len(rJobs))
	for _, job := range rJobs {
		found := false
		for _, testJob := range testJobs {
			if job.ID == testJob.ID {
				found = true
			}

			assert.Equal(t, job.Listener.ID, testJob.Listener.ID)
			assert.Equal(t, data.JobDead, job.Status)
		}
		assert.True(t, found)
	}
	_, err = djRepo.RequeueDeadJobsForConsumer(testJobs[0].Listener)
	assert.Nil(t, err)
	rJobs, _, err = djRepo.GetJobsForConsumer(testJobs[0].Listener, data.JobDead, data.NewPagination(nil, nil))
	assert.Nil(t, err)
	assert.LessOrEqual(t, 0, len(rJobs))
	for _, testJob := range testJobs {
		job, err := djRepo.GetByID(testJob.ID.String())
		assert.Nil(t, err)
		assert.Equal(t, data.JobQueued, job.Status)
		assert.Equal(t, uint(0), job.RetryAttemptCount)
	}
}

func TestUpdateJobTimeout(t *testing.T) {
	djRepo := getDeliverJobRepository()
	msgRepo := getMessageRepository()
	message := getMessageForJob()
	msgRepo.Create(message)
	jobs := getDeliveryJobsInFixture(message)
	err := djRepo.DispatchMessage(message, jobs...)
	assert.Nil(t, err)
	t.Run("IncreaseJobTimeout", func(t *testing.T) {
		t.Parallel()
		tJob := jobs[7]
		dJob, err := djRepo.GetByID(tJob.ID.String())
		assert.Nil(t, err)
		assert.Equal(t, data.JobQueued, dJob.Status)
		assert.Equal(t, uint(0), dJob.IncrementalTimeout)
		oldTime := time.Now()

		dJob.IncrementalTimeout = 100
		err = djRepo.MarkJobInflight(dJob)
		assert.Nil(t, err)
		dJob, err = djRepo.GetByID(tJob.ID.String())
		assert.Nil(t, err)
		assert.Equal(t, data.JobInflight, dJob.Status)
		assert.Equal(t, uint(100), dJob.IncrementalTimeout)
		assert.GreaterOrEqual(t, dJob.UpdatedAt, oldTime)

	})
}

func TestGetJobStatusCountsGroupedByConsumer(t *testing.T) {
	djRepo := getDeliverJobRepository()
	result, err := djRepo.GetJobStatusCountsGroupedByConsumer()
	assert.Nil(t, err)
	assert.Equal(t, 1, len(result))
	// The following is an output from a sample run
	// This is predictable since consumer ids are in alphabetical order in IDs
	// Also since this is the last test and not parallel the state after the
	// the package tests are executed should be same
	// `map[channel1-for-consumer:
	// 	map[ctjsidj3occ10ad6kjq0:[QUEUED: 9 INFLIGHT: 1]
	// 		ctjsidj3occ10ad6kjqg:[QUEUED: 9 INFLIGHT: 1]
	// 		ctjsidj3occ10ad6kjr0:[QUEUED: 9 DEAD: 1]
	// 		ctjsidj3occ10ad6kjrg:[QUEUED: 9 DELIVERED: 1]
	// 		ctjsidj3occ10ad6kjs0:[QUEUED: 10]
	// 		ctjsidj3occ10ad6kjsg:[QUEUED: 8 INFLIGHT: 2]
	// 		ctjsidj3occ10ad6kjt0:[QUEUED: 10]
	// 		ctjsidj3occ10ad6kjtg:[QUEUED: 9 INFLIGHT: 1]
	// 		ctjsidj3occ10ad6kju0:[QUEUED: 10]
	// 		ctjsidj3occ10ad6kjug:[QUEUED: 10]]]`
	channelID := Channel_ID(`channel1-for-consumer`)
	assert.Equal(t, 10, len(result[channelID]))
	consumerIds := make([]Consumer_ID, 0, len(result[channelID]))
	for consumerID := range result[channelID] {
		consumerIds = append(consumerIds, consumerID)
	}
	slices.Sort(consumerIds)
	assert.Equal(t, data.JobQueued, result[channelID][consumerIds[0]][0].Status)
	assert.Equal(t, 9, result[channelID][consumerIds[0]][0].Count)
	assert.Equal(t, data.JobInflight, result[channelID][consumerIds[0]][1].Status)
	assert.Equal(t, 1, result[channelID][consumerIds[0]][1].Count)
	assert.Equal(t, data.JobQueued, result[channelID][consumerIds[1]][0].Status)
	assert.Equal(t, 9, result[channelID][consumerIds[1]][0].Count)
	assert.Equal(t, data.JobDead, result[channelID][consumerIds[2]][1].Status)
	assert.Equal(t, 1, result[channelID][consumerIds[2]][1].Count)
	assert.Equal(t, data.JobDelivered, result[channelID][consumerIds[3]][1].Status)
	assert.Equal(t, 1, result[channelID][consumerIds[3]][1].Count)
	assert.Equal(t, 10, result[channelID][consumerIds[4]][0].Count)
	assert.Equal(t, data.JobQueued, result[channelID][consumerIds[4]][0].Status)
}

func TestDeleteDeadJob(t *testing.T) {
	djRepo := getDeliverJobRepository()
	msgRepo := getMessageRepository()
	message := getMessageForJob()
	msgRepo.Create(message)
	jobs := getDeliveryJobsInFixture(message)
	err := djRepo.DispatchMessage(message, jobs...)
	assert.NoError(t, err)

	targetJob := jobs[0]

	t.Run("DeleteNonDeadJobReturns0", func(t *testing.T) {
		// Job is queued, not dead
		rowsAffected, err := djRepo.DeleteDeadJob(targetJob, 0)
		assert.NoError(t, err)
		assert.Equal(t, int64(0), rowsAffected)
	})

	t.Run("DeleteDeadJobWithRetryNotExhausted", func(t *testing.T) {
		err := djRepo.MarkJobInflight(targetJob)
		assert.NoError(t, err)
		err = djRepo.MarkJobDead(targetJob)
		assert.NoError(t, err)
		// retryAttemptCount is 1 but we require >= 5
		rowsAffected, err := djRepo.DeleteDeadJob(targetJob, 5)
		assert.NoError(t, err)
		assert.Equal(t, int64(0), rowsAffected)
	})

	t.Run("DeleteDeadJobSuccess", func(t *testing.T) {
		// retryAttemptCount is 1, require >= 0
		rowsAffected, err := djRepo.DeleteDeadJob(targetJob, 0)
		assert.NoError(t, err)
		assert.Equal(t, int64(1), rowsAffected)
		// Verify it's gone
		_, err = djRepo.GetByID(targetJob.ID.String())
		assert.Error(t, err)
	})
}

func TestDeleteDeadJobsForConsumer(t *testing.T) {
	djRepo := getDeliverJobRepository()
	msgRepo := getMessageRepository()

	// Create 2 messages and dispatch, mark dead for consumer[1]
	consumer := consumers[1]
	for i := 0; i < 2; i++ {
		msg := getMessageForJob()
		msgRepo.Create(msg)
		job, _ := data.NewDeliveryJob(msg, consumer)
		djRepo.DispatchMessage(msg, job)
		djRepo.MarkJobInflight(job)
		djRepo.MarkJobDead(job)
	}

	t.Run("BulkDeleteSuccess", func(t *testing.T) {
		rowsAffected, err := djRepo.DeleteDeadJobsForConsumer(consumer, 0)
		assert.NoError(t, err)
		assert.GreaterOrEqual(t, rowsAffected, int64(2))
	})

	t.Run("NoDeadJobsReturns0", func(t *testing.T) {
		rowsAffected, err := djRepo.DeleteDeadJobsForConsumer(consumer, 0)
		assert.NoError(t, err)
		assert.Equal(t, int64(0), rowsAffected)
	})
}

func TestGetDeadJobCountsSinceCheckpoint(t *testing.T) {
	djRepo := getDeliverJobRepository()
	msgRepo := getMessageRepository()

	checkpoint := time.Now().Add(-1 * time.Second)

	// Create a job and mark it dead
	msg := getMessageForJob()
	msgRepo.Create(msg)
	consumer := consumers[2]
	job, _ := data.NewDeliveryJob(msg, consumer)
	djRepo.DispatchMessage(msg, job)
	djRepo.MarkJobInflight(job)
	djRepo.MarkJobDead(job)

	counts, err := djRepo.GetDeadJobCountsSinceCheckpoint(checkpoint)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, counts[consumer.ID.String()], int64(1))

	t.Run("FutureCheckpointReturnsZero", func(t *testing.T) {
		futureCounts, err := djRepo.GetDeadJobCountsSinceCheckpoint(time.Now().Add(1 * time.Hour))
		assert.NoError(t, err)
		assert.Equal(t, 0, len(futureCounts))
	})
}

// Generated with assistance from Claude AI
