package controllers

import (
	"testing"

	"github.com/julienschmidt/httprouter"
	"github.com/stretchr/testify/assert"
)

func TestConsumerFormatAsRelativeLink(t *testing.T) {
	controller := NewConsumerController(NewChannelController(nil), nil, nil)
	assert.Equal(t, consumerPath, controller.GetPath())
	assert.Equal(t, "/channel/someChannelId/consumer/someConsumerId", controller.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: "someChannelId"},
		httprouter.Param{Key: consumerIDPathParamKey, Value: "someConsumerId"}))
}

func TestConsumersFormatAsRelativeLink(t *testing.T) {
	controller := NewConsumersController(NewChannelController(nil), nil, nil)
	assert.Equal(t, consumersPath, controller.GetPath())
	assert.Equal(t, "/channel/someChannelId/consumers", controller.FormatAsRelativeLink(httprouter.Param{Key: channelIDPathParamKey, Value: "someChannelId"}))
}
