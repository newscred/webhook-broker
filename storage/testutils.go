package storage

import (
	"net/url"
	"strconv"
	"time"

	data "github.com/newscred/webhook-broker/storage/data"
	"github.com/rs/zerolog/log"
)

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
				err := deliverJobRepo.MarkJobInflight(jobs[index])
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
