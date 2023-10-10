package main

import (
	"dozer-stampede/internal"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rs/xid"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
)

func TestNewServer(t *testing.T) {
	defer os.Remove("log.db")

	srv, err := newServer()
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	err = srv.db.Close()
	if err != nil {
		t.Fatalf("failed to close database: %v", err)
	}
}

func TestReportMessage(t *testing.T) {
	srv, err := newServer()
	srv.db.Close()
	os.Remove("log.db")

	require.NoError(t, err)

	t.Run("Successful", func(t *testing.T) {
		msg := &internal.Message{
			Id:     xid.New().String(),
			Period: 100,
		}

		srv.msgQueue <- msg

		req := httptest.NewRequest("POST", "/report", strings.NewReader(`"successful"`))
		r := httptest.NewRecorder()
		srv.reportMessage(r, req)

		require.Equal(t, http.StatusOK, r.Code)

		srv.reportedMsgsMtx.Lock()
		_, data := srv.reportedMsgs["successful"]
		srv.reportedMsgsMtx.Unlock()
		require.True(t, data)
	})

	t.Run("Erroneous", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/report", strings.NewReader(`erroneous`))
		r := httptest.NewRecorder()
		srv.reportMessage(r, req)

		require.Equal(t, http.StatusBadRequest, r.Code)
	})
}

func TestRemoveClient(t *testing.T) {
	clients := []*client{
		{
			Id:         42,
			MsgChan:    make(chan *internal.Message),
			disconnect: make(chan bool),
		},
		{
			Id:         69,
			MsgChan:    make(chan *internal.Message),
			disconnect: make(chan bool),
		},
		{
			Id:         1337,
			MsgChan:    make(chan *internal.Message),
			disconnect: make(chan bool),
		},
	}

	removedClientId := 69
	updatedClients := removeClient(clients, removedClientId)

	for _, client := range updatedClients {
		if client.Id == removedClientId {
			t.Errorf("failed to remove client %d", removedClientId)
		}
	}
}

func TestStoreMessage(t *testing.T) {
	msg := &internal.Message{
		Id:     xid.New().String(),
		Period: uint64(rand.Intn(1000) + 1),
	}

	db, _ := bbolt.Open("log.db", 0600, nil)
	defer os.Remove("log.db")
	defer db.Close()

	err := db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte("log"))
		return err
	})

	if err != nil {
		t.Fatalf("failed to update database: %v", err)
	}

	srv := &server{
		db: db,
	}
	defer srv.db.Close()

	err = srv.storeMessage(msg)
	if err != nil {
		t.Fatalf("filed to store message: %v", err)
	}

	err = db.View(func(tx *bbolt.Tx) error {
		cont := tx.Bucket([]byte("log"))
		if cont == nil {
			return fmt.Errorf("failed to find database bucket %q", "log")
		}

		mes := cont.Get([]byte(msg.Id))
		if mes == nil {
			return fmt.Errorf("failed to get message %s", msg.Id)
		}
		return nil
	})

	if err != nil {
		t.Fatalf("failed to read from database: %v", err)
	}
}

func TestProcessMessages(t *testing.T) {
	srv, err := newServer()
	require.NoError(t, err, "failed to create server")

	defer os.Remove("log.db")
	defer srv.db.Close()

	msgs := []*internal.Message{
		{
			Id:     xid.New().String(),
			Period: uint64(time.Second),
		},
	}

	client := &client{
		Id:         0,
		MsgChan:    make(chan *internal.Message),
		disconnect: make(chan bool),
	}

	srv.clients = append(srv.clients, client)

	done := make(chan bool)

	go func() {
		for msg := range client.MsgChan {
			for _, m := range msgs {
				if m.Id == msg.Id {
					done <- true
				}
			}
		}
		close(done)
	}()

	for _, msg := range msgs {
		srv.msgQueue <- msg
		ok := <-done
		require.True(t, ok, "failed to send message")
	}

	require.Equal(t, len(srv.sentMsgs), len(msgs), "all messages were sent")
	require.Equal(t, len(client.MsgChan), 0, "message channel is empty")
}
