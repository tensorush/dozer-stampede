package main

import (
	"strconv"
	"testing"

	"github.com/r3labs/sse/v2"
	"github.com/stretchr/testify/assert"
)

func TestNewClient(t *testing.T) {
	batchSize := 1337

	expectedUrl := "http://localhost:8080/task?batchsize=" + strconv.Itoa(batchSize)
	client := sse.NewClient("http://localhost:8080/task?batchsize=" + strconv.Itoa(batchSize))

	if client.URL != expectedUrl {
		t.Errorf("unexpected client url, expected: %v, received: %v", expectedUrl, client.URL)
	}

	assert.Equal(t, client.URL, expectedUrl)
}
