package prune

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/newscred/webhook-broker/config"
	configmocks "github.com/newscred/webhook-broker/config/mocks"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	storagemocks "github.com/newscred/webhook-broker/storage/mocks"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

const (
	retentionInDays = 7
)

var (
	migrationLocation, _ = filepath.Abs("../migration/sqls/")
	defaultMigrationConf = &storage.MigrationConfig{MigrationEnabled: true, MigrationSource: "file://" + migrationLocation}
	dataAccessor         storage.DataAccessor
	dbConfig             *config.Config
)

func TestMain(m *testing.M) {
	// Setup DB and migration
	os.Remove("./webhook-broker.sqlite3")
	dbConfig, _ = config.GetAutoConfiguration()
	var dbErr error
	dataAccessor, dbErr = storage.GetNewDataAccessor(dbConfig, defaultMigrationConf, dbConfig)
	if dbErr == nil {
		defer dataAccessor.Close()
		m.Run()
	}
}

func getMockedPruneConfig(t *testing.T) *configmocks.MessagePruningConfig {
	return getConfigurableMockedPruneConfig(t, true)
}

func getConfigurableMockedPruneConfig(t *testing.T, pruneEnabled bool) *configmocks.MessagePruningConfig {
	mockedPruneConfig := new(configmocks.MessagePruningConfig)
	safeTestName := strings.ReplaceAll(t.Name(), "/", "_")
	mockedPruneConfig.On("GetExportNodeName").Return(safeTestName + "_testdump")
	cwd, _ := os.Getwd()
	absoluteCwd, err := filepath.Abs(cwd)
	if err != nil {
		t.Fatal(err)
	}
	log.Info().Msgf("Absolute CWD: %s", absoluteCwd)
	log.Info().Msgf("Safe Test Name: %s", safeTestName)
	mockedPruneConfig.On("GetExportPath").Return(absoluteCwd)
	mockedPruneConfig.On("GetMaxArchiveFileSizeInMB").Return(uint(10))
	mockedPruneConfig.On("GetMessageRetentionDays").Return(uint(retentionInDays))
	fileURL := &url.URL{Scheme: "file", Path: absoluteCwd}
	mockedPruneConfig.On("GetRemoteExportURL").Return(fileURL)
	mockedPruneConfig.On("GetRemoteFilePrefix").Return(".unit-test-data")
	mockedPruneConfig.On("IsPruningEnabled").Return(pruneEnabled)
	return mockedPruneConfig
}

func createMockDataAccessorWrapper(dataAccessor storage.DataAccessor, msgRepo storage.MessageRepository, djRepo storage.DeliveryJobRepository) *storagemocks.DataAccessor {
	mockDataAccessor := new(storagemocks.DataAccessor)
	times := 3
	if djRepo == nil {
		mockDataAccessor.On("GetDeliveryJobRepository").Return(dataAccessor.GetDeliveryJobRepository()).Times(times)
	} else {
		mockDataAccessor.On("GetDeliveryJobRepository").Return(djRepo).Times(times)
	}
	if msgRepo == nil {
		mockDataAccessor.On("GetMessageRepository").Return(dataAccessor.GetMessageRepository()).Times(times)
	} else {
		mockDataAccessor.On("GetMessageRepository").Return(msgRepo).Times(times)
	}
	return mockDataAccessor
}

func TestPruneMessages(t *testing.T) {
	producer, channel, consumers := storage.SetupMessageDependencyFixture(dataAccessor.GetProducerRepository(), dataAccessor.GetChannelRepository(), dataAccessor.GetConsumerRepository(), "prune-service-consumer")
	t.Run("Success", func(t *testing.T) {
		pruneConfig := getMockedPruneConfig(t)
		storage.SetupPruneableMessageFixture(dataAccessor, channel, producer, consumers, (24*retentionInDays+1)*60*60)
		storage.SetupPruneableMessageFixture(dataAccessor, channel, producer, consumers, (24*retentionInDays+1)*60*60)
		storage.SetupPruneableMessageFixture(dataAccessor, channel, producer, consumers, (24*retentionInDays+1)*60*60)
		msgs, _, err := dataAccessor.GetMessageRepository().GetMessagesForChannel(channel.ChannelID, &data.Pagination{})
		assert.Nil(t, err)
		assert.Len(t, msgs, 3)
		err = PruneMessages(dataAccessor, pruneConfig)
		assert.Nil(t, err)
		msgs, _, err = dataAccessor.GetMessageRepository().GetMessagesForChannel(channel.ChannelID, &data.Pagination{})
		assert.Nil(t, err)
		assert.Len(t, msgs, 0)
	})
	storage.SetupPruneableMessageFixture(dataAccessor, channel, producer, consumers, (24*retentionInDays+1)*60*60)
	msgs, _, err := dataAccessor.GetMessageRepository().GetMessagesForChannel(channel.ChannelID, &data.Pagination{})
	assert.Nil(t, err)
	assert.Len(t, msgs, 1)
	anyMatcher := func(arg interface{}) bool { return true }
	t.Run("PruneDisabled", func(t *testing.T) {
		pruneConfig := getConfigurableMockedPruneConfig(t, false)
		err := PruneMessages(dataAccessor, pruneConfig)
		assert.Nil(t, err)
		msgs, _, err = dataAccessor.GetMessageRepository().GetMessagesForChannel(channel.ChannelID, &data.Pagination{})
		assert.Nil(t, err)
		assert.Len(t, msgs, 1)
	})
	t.Run("JobQueryError", func(t *testing.T) {
		originalGetJobs := getJobs
		defer func() { getJobs = originalGetJobs }()
		getJobs = func(dataAccessor storage.DataAccessor, message *data.Message) ([]*data.DeliveryJob, error) {
			log.Info().Msgf("Getting MOCK jobs for message %s", message.ID)
			return nil, assert.AnError
		}
		err := PruneMessages(dataAccessor, getMockedPruneConfig(t))
		assert.Equal(t, assert.AnError, err)
		msgs, _, err = dataAccessor.GetMessageRepository().GetMessagesForChannel(channel.ChannelID, &data.Pagination{})
		assert.Nil(t, err)
		assert.Len(t, msgs, 1)
	})
	t.Run("MessageDeleteError", func(t *testing.T) {
		messageIDMatcher := func (msgs []*data.Message) func(interface{}) bool {
			expectedIDs := make(map[string]bool)
			for _, msg := range msgs {
				expectedIDs[msg.ID.String()] = true
			}
			return func(arg interface{}) bool {
				ids, ok := arg.([]string)
				if !ok {
					return false
				}
				if len(ids) != len(msgs) {
					return false
				}
				for _, id := range ids {
					if _, found := expectedIDs[id]; !found {
						return false
					}
				}
				return true
			}
		}
		mockedMsgRepo := new(storagemocks.MessageRepository)
		mockedMsgRepo.On("DeleteMessagesAndJobs", mock.MatchedBy(anyMatcher), mock.MatchedBy(messageIDMatcher(msgs))).Return(assert.AnError)
		mockedMsgRepo.On("GetMessagesFromBeforeDurationThatAreCompletelyDelivered", time.Duration(retentionInDays*24*60*60)*time.Second, 1000).Return(msgs).Times(1)
		mockDataAccessor := createMockDataAccessorWrapper(dataAccessor, mockedMsgRepo, nil)
		err = PruneMessages(mockDataAccessor, getMockedPruneConfig(t))
		assert.Equal(t, assert.AnError, err)
	})
	t.Run("ArchiveError", func(t *testing.T) {
		originalArchiveMsg := archiveMessage
		defer func() { archiveMessage = originalArchiveMsg }()
		archiveMessage = func(message *data.Message, jobs []*data.DeliveryJob, archiveDirector *ArchiveDirector) error {
			log.Info().Msgf("Archiving MOCK message %s", message.ID)
			return assert.AnError
		}
		err := PruneMessages(dataAccessor, getMockedPruneConfig(t))
		assert.Equal(t, assert.AnError, errors.Unwrap(err))
		msgs, _, err = dataAccessor.GetMessageRepository().GetMessagesForChannel(channel.ChannelID, &data.Pagination{})
		assert.Nil(t, err)
		assert.Len(t, msgs, 1)
	})
	PruneMessages(dataAccessor, getMockedPruneConfig(t))
	msgs, _, err = dataAccessor.GetMessageRepository().GetMessagesForChannel(channel.ChannelID, &data.Pagination{})
	assert.Nil(t, err)
	assert.Len(t, msgs, 0)
}

func TestArchiveDirector_Close(t *testing.T) {
	t.Run("RemoteArchiveManagerNotNil", func(t *testing.T) {
		pruneConfig := getMockedPruneConfig(t)
		director, err := initArchiveDirector(pruneConfig)
		assert.Nil(t, err)
		director.Close()
	})
	t.Run("RemoteArchiveManagerNil", func(t *testing.T) {
		pruneConfig := getMockedPruneConfig(t)
		director, err := initArchiveDirector(pruneConfig)
		director.RemoteArchiveManager = nil
		assert.Nil(t, err)
		director.Close()
	})
}
