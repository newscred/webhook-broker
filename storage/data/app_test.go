package data

import (
	"testing"

	"github.com/imyousuf/webhook-broker/config"
	"github.com/stretchr/testify/assert"
)

func TestNewApp(t *testing.T) {
	t.Parallel()
	configuration, _ := config.GetAutoConfiguration()
	seedData := configuration.GetSeedData()
	app := NewApp(&seedData, Initialized)
	assert.NotNil(t, app)
	assert.Equal(t, &seedData, app.GetSeedData())
	assert.Equal(t, Initialized, app.GetStatus())
}
