package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/newscred/webhook-broker/storage/data"
)

// ScheduledMessageDBRepository is the ScheduledMessageRepository implementation
type ScheduledMessageDBRepository struct {
	db                 *sql.DB
	channelRepository  ChannelRepository
	producerRepository ProducerRepository
}

var (
	// ErrDuplicateScheduledMessageIDForChannel represents when a scheduled message with same ID already exists
	ErrDuplicateScheduledMessageIDForChannel = errors.New("duplicate scheduled message id for channel")
	scheduledMessageMysqlErrorMap            = map[uint16]error{
		// https://dev.mysql.com/doc/mysql-errors/8.0/en/server-error-reference.html#error_er_dup_entry
		1062: ErrDuplicateScheduledMessageIDForChannel,
	}
)

const (
	scheduledMessageSelectRowCommonQuery = "SELECT id, messageId, producerId, channelId, payload, contentType, priority, status, dispatchSchedule, dispatchedAt, headers, createdAt, updatedAt FROM scheduled_message WHERE"
)

// Create creates a new scheduled message if message.MessageID does not already exist; please ensure QuickFix is called before repo is called
func (msgRepo *ScheduledMessageDBRepository) Create(message *data.ScheduledMessage) (err error) {
	if !message.IsInValidState() {
		err = data.ErrInsufficientInformationForCreating
	}
	if err == nil {
		_, msgErr := msgRepo.Get(message.GetChannelIDSafely(), message.MessageID)
		if msgErr == nil {
			err = ErrDuplicateScheduledMessageIDForChannel
		} else {
			err = transactionalSingleRowWriteExec(msgRepo.db, emptyOps, "INSERT INTO scheduled_message (id, channelId, producerId, messageId, payload, contentType, priority, status, dispatchSchedule, dispatchedAt, headers, createdAt, updatedAt) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
				args2SliceFnWrapper(message.ID, message.BroadcastedTo.ChannelID, message.ProducedBy.ProducerID, message.MessageID, message.Payload, message.ContentType, message.Priority, message.Status, message.DispatchSchedule, sql.NullTime{Time: message.DispatchedAt, Valid: !message.DispatchedAt.IsZero()}, message.Headers, message.CreatedAt, message.UpdatedAt))
			err = normalizeDBError(err, scheduledMessageMysqlErrorMap)
		}
	}
	return err
}

// Get retrieves a scheduled message for a channel if it exists
func (msgRepo *ScheduledMessageDBRepository) Get(channelID string, messageID string) (*data.ScheduledMessage, error) {
	channel, err := msgRepo.channelRepository.Get(channelID)
	if err != nil {
		return &data.ScheduledMessage{}, err
	}
	message, err := msgRepo.getSingleMessage(scheduledMessageSelectRowCommonQuery+" channelId like ? and messageId like ?", args2SliceFnWrapper(channelID, messageID), false)
	if err == nil {
		message.BroadcastedTo = channel
	}
	return message, err
}

// GetByID retrieves a scheduled message by its ID
func (msgRepo *ScheduledMessageDBRepository) GetByID(id string) (*data.ScheduledMessage, error) {
	return msgRepo.getSingleMessage(scheduledMessageSelectRowCommonQuery+" id like ?", args2SliceFnWrapper(id), true)
}

func (msgRepo *ScheduledMessageDBRepository) getSingleMessage(query string, queryArgs func() []interface{}, loadChannel bool) (message *data.ScheduledMessage, err error) {
	var producerID string
	var channelID string
	var nullDispatchedDate sql.NullTime
	message = &data.ScheduledMessage{}
	if err == nil {
		err = querySingleRow(msgRepo.db, query, queryArgs,
			args2SliceFnWrapper(&message.ID, &message.MessageID, &producerID, &channelID, &message.Payload, &message.ContentType, &message.Priority, &message.Status, &message.DispatchSchedule, &nullDispatchedDate, &message.Headers, &message.CreatedAt, &message.UpdatedAt))
	}
	if err == nil {
		message.ProducedBy, err = msgRepo.producerRepository.Get(producerID)
		if nullDispatchedDate.Valid {
			message.DispatchedAt = nullDispatchedDate.Time
		}
	}
	if loadChannel && err == nil {
		message.BroadcastedTo, err = msgRepo.channelRepository.Get(channelID)
	}
	return message, err
}

// MarkDispatched marks a scheduled message as dispatched with the current time
func (msgRepo *ScheduledMessageDBRepository) MarkDispatched(message *data.ScheduledMessage) error {
	if message.Status != data.ScheduledMsgStatusDispatched {
		return fmt.Errorf("can only mark scheduled messages with status DISPATCHED; current status - %v", message.Status)
	}

	err := transactionalSingleRowWriteExec(msgRepo.db, emptyOps, "UPDATE scheduled_message SET status = ?, dispatchedAt = ?, updatedAt = ? WHERE id = ?",
		args2SliceFnWrapper(message.Status, message.DispatchedAt, time.Now(), message.ID))

	return err
}

// GetMessagesReadyForDispatch retrieves scheduled messages that are ready to be dispatched
func (msgRepo *ScheduledMessageDBRepository) GetMessagesReadyForDispatch(limit int) []*data.ScheduledMessage {
	var messages []*data.ScheduledMessage
	query := fmt.Sprintf("%s status = ? AND dispatchSchedule <= ? ORDER BY dispatchSchedule ASC LIMIT %d", scheduledMessageSelectRowCommonQuery, limit)
	log.Debug().Str("query", query).Int("limit", limit).Msg("Querying for scheduled messages ready for dispatch")
	rows, err := msgRepo.db.Query(query, data.ScheduledMsgStatusScheduled.GetValue(), time.Now())
	if err != nil {
		log.Error().Err(err).Msg("Error querying for scheduled messages ready for dispatch")
		return messages
	}
	defer rows.Close()
	for rows.Next() {
		var producerID string
		var channelID string
		var nullDispatchedDate sql.NullTime
		message := &data.ScheduledMessage{}
		err := rows.Scan(&message.ID, &message.MessageID, &producerID, &channelID, &message.Payload, &message.ContentType, &message.Priority, &message.Status, &message.DispatchSchedule, &nullDispatchedDate, &message.Headers, &message.CreatedAt, &message.UpdatedAt)
		if err != nil {
			log.Error().Err(err).Msg("Error scanning scheduled message")
			continue
		}
		if nullDispatchedDate.Valid {
			message.DispatchedAt = nullDispatchedDate.Time
		}
		message.ProducedBy, err = msgRepo.producerRepository.Get(producerID)
		if err != nil {
			log.Error().Err(err).Str("producerId", producerID).Msg("Error retrieving producer for scheduled message")
			continue
		}
		message.BroadcastedTo, err = msgRepo.channelRepository.Get(channelID)
		if err != nil {
			log.Error().Err(err).Str("channelId", channelID).Msg("Error retrieving channel for scheduled message")
			continue
		}
		messages = append(messages, message)
	}
	return messages
}

// GetScheduledMessagesForChannel retrieves scheduled messages for a specific channel with optional status filters
func (msgRepo *ScheduledMessageDBRepository) GetScheduledMessagesForChannel(channelID string, page *data.Pagination, statusFilters ...data.ScheduledMsgStatus) ([]*data.ScheduledMessage, *data.Pagination, error) {
	emptyMessages := make([]*data.ScheduledMessage, 0)
	defaultEmptyPagination := &data.Pagination{}

	// Validate pagination object
	if page == nil || (page.Next != nil && page.Previous != nil) {
		return emptyMessages, defaultEmptyPagination, ErrPaginationDeadlock
	}

	// Verify channel exists
	_, err := msgRepo.channelRepository.Get(channelID)
	if err != nil {
		return emptyMessages, defaultEmptyPagination, err
	}

	// Build status filter condition
	statusCondition := ""
	statusFilterValues := make([]interface{}, 0, len(statusFilters))
	if len(statusFilters) > 0 {
		statusCondition = "AND status IN ("
		placeholders := make([]string, 0, len(statusFilters))
		for _, status := range statusFilters {
			placeholders = append(placeholders, "?")
			statusFilterValues = append(statusFilterValues, status.GetValue())
		}
		statusCondition += strings.Join(placeholders, ",") + ")"
	}

	// Base query with pagination
	baseQuery := fmt.Sprintf("%s channelId = ? %s", scheduledMessageSelectRowCommonQuery, statusCondition)

	// Add pagination parameters
	baseQuery += getPaginationQueryFragmentWithConfigurablePageSize(page, true, pageSizeWithOrder)

	// Create parameter list with pagination args
	params := append([]interface{}{channelID}, statusFilterValues...)
	params = appendWithPaginationArgs(page, params...)

	// Execute query and get results
	messages, newPagination, err := msgRepo.getScheduledMessages(baseQuery, params...)
	if err != nil {
		log.Error().Err(err).Msg("Error retrieving scheduled messages")
	}

	return messages, newPagination, err
}

// getScheduledMessages executes a query and returns scheduled messages with pagination
func (msgRepo *ScheduledMessageDBRepository) getScheduledMessages(baseQuery string, args ...interface{}) ([]*data.ScheduledMessage, *data.Pagination, error) {
	pageMessages := make([]*data.ScheduledMessage, 0, 25)
	newPage := &data.Pagination{}

	// Extract channel ID from args for later use
	var channelIDArg string
	if len(args) > 0 {
		if id, ok := args[0].(string); ok {
			channelIDArg = id
		}
	}

	// Scan function that also captures producer and channel IDs
	rows, err := msgRepo.db.Query(baseQuery, args...)
	if err != nil {
		log.Error().Err(err).Msg("Error executing query for scheduled messages")
		return pageMessages, newPage, err
	}
	defer rows.Close()

	for rows.Next() {
		msg := &data.ScheduledMessage{}
		var producerID string
		var channelID string
		var nullDispatchedDate sql.NullTime

		err := rows.Scan(
			&msg.ID,
			&msg.MessageID,
			&producerID,
			&channelID,
			&msg.Payload,
			&msg.ContentType,
			&msg.Priority,
			&msg.Status,
			&msg.DispatchSchedule,
			&nullDispatchedDate,
			&msg.Headers,
			&msg.CreatedAt,
			&msg.UpdatedAt,
		)
		if err != nil {
			log.Error().Err(err).Msg("Error scanning scheduled message row")
			continue
		}

		if nullDispatchedDate.Valid {
			msg.DispatchedAt = nullDispatchedDate.Time
		}

		// Load producer
		if producerID != "" {
			var err error
			msg.ProducedBy, err = msgRepo.producerRepository.Get(producerID)
			if err != nil {
				log.Error().Err(err).Str("producerId", producerID).Msg("Error loading producer for scheduled message")
			}
		}

		// Load channel - try channelID from row first, then fallback to args
		if channelID != "" {
			var err error
			msg.BroadcastedTo, err = msgRepo.channelRepository.Get(channelID)
			if err != nil {
				log.Error().Err(err).Str("channelId", channelID).Msg("Error loading channel for scheduled message")
			}
		} else if channelIDArg != "" {
			var err error
			msg.BroadcastedTo, err = msgRepo.channelRepository.Get(channelIDArg)
			if err != nil {
				log.Error().Err(err).Str("channelId", channelIDArg).Msg("Error loading channel for scheduled message")
			}
		}

		pageMessages = append(pageMessages, msg)
	}

	if err = rows.Err(); err != nil {
		log.Error().Err(err).Msg("Error iterating over scheduled message rows")
		return pageMessages, newPage, err
	}

	// Create pagination object for next/previous pages
	msgCount := len(pageMessages)
	if msgCount > 0 {
		newPage = data.NewPagination(pageMessages[msgCount-1], pageMessages[0])
	}

	return pageMessages, newPage, nil
}

// GetScheduledMessageStatusCountsByChannel retrieves counts of scheduled messages by status for a specific channel
func (msgRepo *ScheduledMessageDBRepository) GetScheduledMessageStatusCountsByChannel(channelID string) ([]*data.StatusCount[data.ScheduledMsgStatus], error) {
	var statusCounts []*data.StatusCount[data.ScheduledMsgStatus]
	query := "SELECT status, COUNT(*) FROM scheduled_message WHERE channelId = ? GROUP BY status"
	rows, err := msgRepo.db.Query(query, channelID)
	if err != nil {
		log.Error().Err(err).Str("query", query).Msg("Error querying for scheduled message status counts")
		return statusCounts, err
	}
	defer rows.Close()

	for rows.Next() {
		var status int
		var count int
		err := rows.Scan(&status, &count)
		if err != nil {
			log.Error().Err(err).Msg("Error scanning scheduled message status count")
			continue
		}
		statusCounts = append(statusCounts, &data.StatusCount[data.ScheduledMsgStatus]{
			Status: data.ScheduledMsgStatus(status),
			Count:  count,
		})
	}

	return statusCounts, nil
}

// GetNextScheduledMessageTime retrieves the earliest dispatch time for a scheduled message in SCHEDULED status
func (msgRepo *ScheduledMessageDBRepository) GetNextScheduledMessageTime(channelID string) (*time.Time, error) {
	var dispatchSchedule time.Time
	query := "SELECT dispatchSchedule FROM scheduled_message WHERE channelId = ? AND status = ? ORDER BY dispatchSchedule ASC LIMIT 1"
	err := msgRepo.db.QueryRow(query, channelID, data.ScheduledMsgStatusScheduled.GetValue()).Scan(&dispatchSchedule)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &dispatchSchedule, nil
}

// NewScheduledMessageRepository creates new scheduled message repository with the DB
func NewScheduledMessageRepository(db *sql.DB, channelRepo ChannelRepository, producerRepo ProducerRepository) ScheduledMessageRepository {
	return &ScheduledMessageDBRepository{db: db, channelRepository: channelRepo, producerRepository: producerRepo}
}