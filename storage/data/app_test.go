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

func TestAppStatusDBDriverFuncs(t *testing.T) {
	var status AppStatus = Initializing
	t.Run("Scan", func(t *testing.T) {
		t.Parallel()
		var input AppStatus = AppStatus(0)
		var sInput *AppStatus = &input
		sInput.Scan(int64(int(status)))
		assert.Equal(t, *sInput, status)
	})
	t.Run("Value", func(t *testing.T) {
		t.Parallel()
		outStatus, _ := status.Value()
		assert.Equal(t, int64(int(status)), outStatus.(int64))
	})
}
