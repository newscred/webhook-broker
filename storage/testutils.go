package storage

import (
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"time"

	data "github.com/newscred/webhook-broker/storage/data"
	"github.com/rs/zerolog/log"
)

// testDB is already declared in rdbms_test.go

const (
	samplePayload          = "some payload"
	sampleContentType      = "a content type"
	successfulGetTestToken = "sometokenforget"
	consumerIDPrefix       = "test-consumer-for-dj-"
)

var (
	callbackURL *url.URL
)

type DeliveryJobSetupOptions struct {
	ConsumerCount          int
	ConsumerIDPrefix       string
	ConsumerRepo           ConsumerRepository
	IgnoreSettingConsumers bool
	ConsumerChannel        *data.Channel
}

func (opt *DeliveryJobSetupOptions) GetConsumerCount() int {
	if opt.ConsumerCount == 0 {
		return 10
	}
	return opt.ConsumerCount
}

func (opt *DeliveryJobSetupOptions) GetConsumerIDPrefix() string {
	if opt.ConsumerIDPrefix == "" {
		return consumerIDPrefix
	}
	return opt.ConsumerIDPrefix
}

func (opt *DeliveryJobSetupOptions) GetConsumerRepo() ConsumerRepository {
	return opt.ConsumerRepo
}

func (opt *DeliveryJobSetupOptions) GetConsumerChannel() *data.Channel {
	return opt.ConsumerChannel
}

func SetupForDeliveryJobTestsWithOptions(options *DeliveryJobSetupOptions) []*data.Consumer {
	testConsumers := options.GetConsumerCount()
	consumerRepo := options.GetConsumerRepo()
	internalConsumers := make([]*data.Consumer, 0, testConsumers)
	if callbackURL == nil {
		callbackURL, _ = url.Parse("https://imytech.net/")
	}
	for i := 0; i < testConsumers; i++ {
		consumer, _ := data.NewConsumer(options.GetConsumerChannel(), options.GetConsumerIDPrefix()+strconv.Itoa(i),
			successfulGetTestToken, callbackURL, "")
		consumer.QuickFix()
		consumerRepo.Store(consumer)
		internalConsumers = append(internalConsumers, consumer)
	}
	return internalConsumers
}

func SetupMessageDependencyFixture(producerRepo ProducerRepository, channelRepo ChannelRepository,
	consumerRepo ConsumerRepository, consumerIDPrefix string) (*data.Producer, *data.Channel, []*data.Consumer) {
	producer, _ := data.NewProducer("producer1-for-message", successfulGetTestToken)
	producer.QuickFix()
	localProducer, _ := producerRepo.Store(producer)
	var err error
	localChannel, _ := data.NewChannel("channel-for-prune", "sampletoken")
	if localChannel, err = channelRepo.Store(localChannel); err != nil {
		log.Fatal().Err(err)
	}
	thisConsumers := SetupForDeliveryJobTestsWithOptions(&DeliveryJobSetupOptions{IgnoreSettingConsumers: true,
		ConsumerCount: 1, ConsumerIDPrefix: consumerIDPrefix, ConsumerChannel: localChannel, ConsumerRepo: consumerRepo})
	return localProducer, localChannel, thisConsumers
}

func SetupPruneableMessageFixture(dataAccessor DataAccessor, channel *data.Channel, producer *data.Producer,
	pruneConsumers []*data.Consumer, lag int) (*data.Message, []*data.DeliveryJob) {
	msg, _ := data.NewMessage(channel, producer, samplePayload, sampleContentType, data.HeadersMap{})
	msg.ReceivedAt = msg.ReceivedAt.Add(time.Duration(-1*lag) * time.Second)
	msg.QuickFix()
	msgRepo := dataAccessor.GetMessageRepository()
	jobs := make([]*data.DeliveryJob, 0, len(pruneConsumers))
	err := msgRepo.Create(msg)
	errMsg := "error creating message"
	if err == nil {
		for _, consumer := range pruneConsumers {
			job, _ := data.NewDeliveryJob(msg, consumer)
			jobs = append(jobs, job)
		}
		deliverJobRepo := dataAccessor.GetDeliveryJobRepository()
		errMsg = "dispatching message failed"
		err = deliverJobRepo.DispatchMessage(msg, jobs...)
		if err == nil {
			for index := range jobs {
				errMsg = "error marking job inflight"
				err = deliverJobRepo.MarkJobInflight(jobs[index])
				if err == nil {
					errMsg = "error marking job delivered"
					err = deliverJobRepo.MarkJobDelivered(jobs[index])
				}
			}
		}
	}
	if err != nil {
		log.Error().Err(err).Msg(errMsg)
	}
	return msg, jobs
}

// NOTE: We're handling the setup in the test functions themselves

// PauseFixture holds an isolated channel, producer and consumers for dispatch pause tests
type PauseFixture struct {
	Channel   *data.Channel
	Producer  *data.Producer
	Push      []*data.Consumer
	Pull      []*data.Consumer
	Consumers []*data.Consumer
}

// SetupPauseFixture creates a channel, a producer and push/pull consumers whose ids start with prefix.
// callbackURLFor returns the callback URL for the i-th consumer (push consumers first, then pull).
func SetupPauseFixture(dataAccessor DataAccessor, prefix string, push, pull int, callbackURLFor func(i int) *url.URL) (*PauseFixture, error) {
	fixture := &PauseFixture{}
	channel, err := data.NewChannel(prefix+"channel", successfulGetTestToken)
	if err != nil {
		return nil, err
	}
	if fixture.Channel, err = dataAccessor.GetChannelRepository().Store(channel); err != nil {
		return nil, err
	}
	producer, err := data.NewProducer(prefix+"producer", successfulGetTestToken)
	if err != nil {
		return nil, err
	}
	producer.QuickFix()
	if fixture.Producer, err = dataAccessor.GetProducerRepository().Store(producer); err != nil {
		return nil, err
	}
	for i := 0; i < push+pull; i++ {
		consumerType := data.PushConsumerStr
		if i >= push {
			consumerType = data.PullConsumerStr
		}
		consumer, consumerErr := data.NewConsumer(fixture.Channel, prefix+"consumer-"+strconv.Itoa(i), successfulGetTestToken, callbackURLFor(i), consumerType)
		if consumerErr != nil {
			return nil, consumerErr
		}
		consumer.QuickFix()
		if consumer, consumerErr = dataAccessor.GetConsumerRepository().Store(consumer); consumerErr != nil {
			return nil, consumerErr
		}
		fixture.Consumers = append(fixture.Consumers, consumer)
		if i < push {
			fixture.Push = append(fixture.Push, consumer)
		} else {
			fixture.Pull = append(fixture.Pull, consumer)
		}
	}
	return fixture, nil
}

// JobPlacement returns how many rows for the (message, consumer) pair (internal ids) exist in job and in job_parking
func JobPlacement(db *sql.DB, messageID, consumerID string) (inJob, inParking int, err error) {
	if err = db.QueryRow("SELECT COUNT(*) FROM job WHERE messageId = ? AND consumerId = ?", messageID, consumerID).Scan(&inJob); err != nil {
		return
	}
	err = db.QueryRow("SELECT COUNT(*) FROM job_parking WHERE messageId = ? AND consumerId = ?", messageID, consumerID).Scan(&inParking)
	return
}

// CheckParkingInvariants verifies that (a) no job id is present in both job and job_parking, (b) no (message,
// consumer) pair is present in both tables and (c) none of neverPausedConsumerIDs (internal ids) has parked rows.
func CheckParkingInvariants(db *sql.DB, neverPausedConsumerIDs ...string) error {
	overlap := 0
	if err := db.QueryRow("SELECT COUNT(*) FROM job j JOIN job_parking p ON j.id = p.id").Scan(&overlap); err != nil {
		return err
	}
	if overlap > 0 {
		return fmt.Errorf("%d job ids present in both job and job_parking", overlap)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM job j JOIN job_parking p ON j.messageId = p.messageId AND j.consumerId = p.consumerId").Scan(&overlap); err != nil {
		return err
	}
	if overlap > 0 {
		return fmt.Errorf("%d message/consumer pairs present in both job and job_parking", overlap)
	}
	for _, consumerID := range neverPausedConsumerIDs {
		parked := 0
		if err := db.QueryRow("SELECT COUNT(*) FROM job_parking WHERE consumerId = ?", consumerID).Scan(&parked); err != nil {
			return err
		}
		if parked > 0 {
			return fmt.Errorf("consumer %s was never paused but has %d parked jobs", consumerID, parked)
		}
	}
	return nil
}

// CheckMessageJobCount verifies the message (internal id) has exactly expected jobs across job and job_parking
func CheckMessageJobCount(db *sql.DB, messageID string, expected int) error {
	inJob, inParking := 0, 0
	if err := db.QueryRow("SELECT COUNT(*) FROM job WHERE messageId = ?", messageID).Scan(&inJob); err != nil {
		return err
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM job_parking WHERE messageId = ?", messageID).Scan(&inParking); err != nil {
		return err
	}
	if inJob+inParking != expected {
		return fmt.Errorf("message %s has %d jobs and %d parked jobs, expected %d in total", messageID, inJob, inParking, expected)
	}
	return nil
}

// InsertPauseRow inserts a dispatch_pause row directly, simulating another node pausing behind a registry's back.
// Pass an empty consumerID for the global pause (scope data.GlobalDispatchPauseScope).
func InsertPauseRow(db *sql.DB, scope, consumerID string) error {
	var consumerRef sql.NullString
	if consumerID != "" {
		consumerRef = sql.NullString{String: consumerID, Valid: true}
	}
	_, err := db.Exec("INSERT INTO dispatch_pause (scope, consumerId, reason, pausedAt) VALUES (?, ?, ?, ?)", scope, consumerRef, "test", time.Now())
	return err
}

// DeletePauseRow deletes a dispatch_pause row directly
func DeletePauseRow(db *sql.DB, scope string) error {
	_, err := db.Exec("DELETE FROM dispatch_pause WHERE scope = ?", scope)
	return err
}

// Generated with assistance from Claude AI
