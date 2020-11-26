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
	oldArgs := os.Args
	os.Args = []string{"webhook-broker"}
	defer func() {
		log.SetOutput(os.Stderr)
		os.Args = oldArgs
	}()
	// main()
	// logString := buf.String()
	// assert.Contains(t, logString, "Webhook Broker")
	// assert.Contains(t, logString, string(GetAppVersion()))
	// t.Log(logString)
}
