package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetAppVersion(t *testing.T) {
	assert.Equal(t, string(GetAppVersion()), "0.1-dev")
}
