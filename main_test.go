package main

import (
	"bytes"
	"log"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetAppVersion(t *testing.T) {
	assert.Equal(t, string(GetAppVersion()), "0.1-dev")
}

func TestMainFunc(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer func() {
		log.SetOutput(os.Stderr)
	}()
	main()
	logString := buf.String()
	assert.Contains(t, logString, "Webhook Broker")
	assert.Contains(t, logString, string(GetAppVersion()))
	t.Log(logString)
}
