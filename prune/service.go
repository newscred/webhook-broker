package prune

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/newscred/webhook-broker/config"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/rs/zerolog/log"
	"gocloud.dev/blob"
)

type ArchiveDirector struct {
	LocalArchiveManager  *ArchiveWriteManager
	RemoteArchiveManager *ArchiveWriteManager
}

func (director *ArchiveDirector) Close() {
	if director.RemoteArchiveManager != nil {
		err := director.RemoteArchiveManager.Close()
		if err != nil {
			log.Error().Err(err).Msg("failed to close remote archive manager")
		}
	}
	err := director.LocalArchiveManager.Close()
	if err != nil {
		log.Error().Err(err).Msg("failed to close local archive manager")
	}
}

func buildRemoteObjectName(pruneConfig config.MessagePruningConfig) string {
	now := time.Now().UTC().Format("2006_01_02T15_04_05Z")
	objectName := fmt.Sprintf("%s_%s.jsonl", pruneConfig.GetExportNodeName(), now)
	if len(pruneConfig.GetRemoteFilePrefix()) > 0 {
		objectName = fmt.Sprintf("%s/%s", pruneConfig.GetRemoteFilePrefix(), objectName)
	}
	return objectName
}

var (
	initLocalArchiveManager = func(pruneConfig config.MessagePruningConfig) (*ArchiveWriteManager, error) {
		now := time.Now().UTC().Format("2006_01_02T15_04_05Z")
		dirPath := fmt.Sprintf("file://%s/%s", pruneConfig.GetExportPath(), pruneConfig.GetRemoteFilePrefix())
		objectName := fmt.Sprintf("local_%s_%s.jsonl", pruneConfig.GetExportNodeName(), now)
		log.Info().Msgf("Local archive path: %s, object name: %s", dirPath, objectName)
		fileBucket, err := blob.OpenBucket(context.Background(), dirPath+"?no_tmp_dir=1")
		if err != nil {
			return nil, fmt.Errorf("failed to open local archive file: %w", err)
		}
		log.Debug().Msgf("Local archive file opened: %s", dirPath)
		return NewArchiveWriteManager(NewBlobBucket(fileBucket),
			objectName, int64(pruneConfig.GetMaxArchiveFileSizeInMB())*1024*1024)
	}

	initRemoteArchiveManager = func(pruneConfig config.MessagePruningConfig) (*ArchiveWriteManager, error) {
		if pruneConfig.GetRemoteExportURL() == nil {
			return nil, nil
		}
		objectName := buildRemoteObjectName(pruneConfig)
		ctx := context.Background()
		var bucket *blob.Bucket
		var err error
		bucket, err = blob.OpenBucket(ctx, pruneConfig.GetRemoteExportURL().String())
		if err != nil {
			return nil, fmt.Errorf("failed to open remote bucket: %w", err)
		}
		return NewArchiveWriteManager(NewBlobBucket(bucket), objectName, int64(pruneConfig.GetMaxArchiveFileSizeInMB())*1024*1024)
	}

	initArchiveDirector = func(config config.MessagePruningConfig) (*ArchiveDirector, error) {
		localArchiveManager, err := initLocalArchiveManager(config)
		if err != nil {
			return nil, err
		}
		log.Info().Msg("Local archive manager initialized")
		remoteArchiveManager, err := initRemoteArchiveManager(config)
		if err != nil {
			return nil, err
		}
		log.Info().Msg("Remote archive manager initialized")
		return &ArchiveDirector{
			LocalArchiveManager:  localArchiveManager,
			RemoteArchiveManager: remoteArchiveManager,
		}, nil
	}

	archiveMessage = func(message *data.Message, jobs []*data.DeliveryJob, director *ArchiveDirector) error {
		archiveData := struct {
			Message *data.Message       `json:"message"`
			Jobs    []*data.DeliveryJob `json:"jobs"`
		}{
			Message: message,
			Jobs:    jobs,
		}
		jsonData, err := json.Marshal(archiveData)
		if err != nil {
			return fmt.Errorf("failed to marshal message and jobs to JSON: %w", err)
		}
		jsonStr := string(jsonData) + "\n"

		_, err = director.LocalArchiveManager.Write(context.Background(), jsonStr)
		if err != nil {
			return fmt.Errorf("failed to write message and jobs to local archive: %w", err)
		}
		if director.RemoteArchiveManager != nil {
			_, err = director.RemoteArchiveManager.Write(context.Background(), jsonStr)
			if err != nil {
				return fmt.Errorf("failed to write message and jobs to remote archive: %w", err)
			}
		}

		return nil
	}

	getJobs = func(dataAccessor storage.DataAccessor, message *data.Message) ([]*data.DeliveryJob, error) {
		jobs := make([]*data.DeliveryJob, 0, 100)
		page := data.NewPagination(nil, nil)
		more := true
		var err error
		for more {
			var jobsPage []*data.DeliveryJob
			jobsPage, page, err = dataAccessor.GetDeliveryJobRepository().GetJobsForMessage(message, page)
			if err != nil {
				err = fmt.Errorf("failed to get jobs for message %d: %w", message.ID, err)
				more = false
			} else if len(jobsPage) > 0 {
				jobs = append(jobs, jobsPage...)
				page.Previous = nil
			} else {
				more = false
			}
		}
		return jobs, err
	}
)

// PruneMessages prunes messages older than the configured retention period.
// It retrieves messages, their associated jobs, archives them, and then deletes them from the database.
// The archiving process is currently a placeholder and needs to be implemented.
// dataAccessor: Accessor for database operations.
// config: Configuration for message pruning.
// Returns an error if any operation fails.
func PruneMessages(dataAccessor storage.DataAccessor, config config.MessagePruningConfig) error {
	// Skip if pruning is disabled
	if !config.IsPruningEnabled() {
		log.Info().Msg("Pruning is disabled, so skipping")
		return nil
	}

	moreMessages := true
	var err error

	archiveDirector, err := initArchiveDirector(config)
	if err != nil {
		return err
	}
	defer archiveDirector.Close()
	log.Debug().Msg("Writes initialized, now loading messages to archive")

	messageIDsToDelete := []string{}
	for moreMessages {
		log.Debug().Msg("Loading messages to archive")
		// Get all messages that are completely delivered for a certain period
		messages := dataAccessor.GetMessageRepository().GetMessagesFromBeforeDurationThatAreCompletelyDelivered(time.Duration(config.GetMessageRetentionDays()*24*60*60)*time.Second, 1000)
		log.Debug().Msgf("Loaded %d messages to archive", len(messages))
		if len(messages) == 0 {
			log.Info().Msg("No messages to prune")
			moreMessages = false
			continue
		}
		log.Debug().Msg("Loading jobs to archive")
		// Get jobs for each message and write to JSON stream
		for _, message := range messages {
			jobs, jobErr := getJobs(dataAccessor, message)
			log.Info().Msgf("Archiving message %s with %d jobs, err: %v", message.ID, len(jobs), jobErr)
			if jobErr != nil {
				log.Error().Err(jobErr).Msgf("failed to get jobs for message %d", message.ID)
				err = jobErr
				moreMessages = false
				break
			}
			if archiveErr := archiveMessage(message, jobs, archiveDirector); archiveErr != nil {
				err = fmt.Errorf("failed to archive message %s: %w", message.ID, archiveErr)
				log.Error().Err(err)
				moreMessages = false
				break
			}
			messageIDsToDelete = append(messageIDsToDelete, message.ID.String())
		}

		if len(messageIDsToDelete) > 0 {
			err = dataAccessor.GetMessageRepository().DeleteMessagesAndJobs(context.Background(), messageIDsToDelete)
			if err != nil {
				log.Error().Err(err).Msg("failed to delete messages in batch")
				return err
			}
			messageIDsToDelete = []string{}
		}
	}
	return err
}
