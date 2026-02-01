package controllers

import (
	"net/http"

	"github.com/julienschmidt/httprouter"
	"github.com/newscred/webhook-broker/storage"
)

const (
	dlqStatusPath = "/dlq-status"
)

// DLQStatusModel represents the response for DLQ status
type DLQStatusModel struct {
	Consumers []*DLQConsumerModel `json:"consumers"`
}

// DLQConsumerModel represents a consumer's dead job count
type DLQConsumerModel struct {
	ChannelID    string `json:"channelId"`
	ChannelName  string `json:"channelName"`
	ConsumerID   string `json:"consumerId"`
	ConsumerName string `json:"consumerName"`
	DeadCount    int64  `json:"deadCount"`
}

// DLQStatusController represents the GET endpoint for DLQ status
type DLQStatusController struct {
	DLQSummaryRepo storage.DLQSummaryRepository
}

// NewDLQStatusController creates a new DLQ status controller
func NewDLQStatusController(dlqSummaryRepo storage.DLQSummaryRepository) *DLQStatusController {
	return &DLQStatusController{DLQSummaryRepo: dlqSummaryRepo}
}

// GetPath returns the endpoint's path
func (controller *DLQStatusController) GetPath() string {
	return dlqStatusPath
}

// FormatAsRelativeLink formats this controller's URL
func (controller *DLQStatusController) FormatAsRelativeLink(params ...httprouter.Param) string {
	return dlqStatusPath
}

// Get implements GET /dlq-status
func (controller *DLQStatusController) Get(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	summaries, err := controller.DLQSummaryRepo.GetAll()
	if err != nil {
		writeErr(w, err)
		return
	}
	consumers := make([]*DLQConsumerModel, 0, len(summaries))
	for _, s := range summaries {
		consumers = append(consumers, &DLQConsumerModel{
			ChannelID:    s.ChannelID,
			ChannelName:  s.ChannelName,
			ConsumerID:   s.ConsumerID,
			ConsumerName: s.ConsumerName,
			DeadCount:    s.DeadCount,
		})
	}
	writeJSON(w, &DLQStatusModel{Consumers: consumers})
}

// Generated with assistance from Claude AI
