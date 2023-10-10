package main

import (
	"dozer-stampede/internal"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.etcd.io/bbolt"

	"github.com/gorilla/mux"
	"github.com/rs/xid"
)

// client contains client data
type client struct {
	Id         int
	MsgChan    chan *internal.Message
	disconnect chan bool
}

// server contains server data
type server struct {
	db              *bbolt.DB
	clients         []*client
	msgQueue        chan *internal.Message
	numMsgRetries   map[string]int
	sentMsgs        map[string]struct{}
	reportedMsgs    map[string]struct{}
	sentMsgsMtx     sync.Mutex
	reportedMsgsMtx sync.Mutex
}

func main() {
	var generatedIds = make(map[string]struct{})

	srv, err := newServer()
	if err != nil {
		log.Fatal("failed to create server: ", err)
	}

	var id string
	for {
		id := xid.New().String()
		if _, ok := generatedIds[id]; !ok {
			generatedIds[id] = struct{}{}
			break
		}
	}

	go func() {
		for {
			period := uint64(rand.Intn(1000) + 1)
			msg := &internal.Message{
				Id:     id,
				Period: period,
			}

			srv.msgQueue <- msg
			time.Sleep(time.Second)
		}
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		log.Printf("shutting down server")
		srv.db.Close()
		os.Exit(0)
	}()

	router := mux.NewRouter()
	router.HandleFunc("/task", srv.sendMessages).Methods("GET")
	router.HandleFunc("/report", srv.reportMessage).Methods("POST")

	log.Printf("server was launched at http://localhost:8080")
	log.Fatal("failed to serve: ", http.ListenAndServe(":8080", router))
}

func newServer() (*server, error) {
	db, err := bbolt.Open("log.db", 0600, nil)
	if err != nil {
		log.Fatal("failed to open database: ", err)
		return nil, err
	}

	err = db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte("log"))
		if err != nil {
			log.Fatal("failed to create bucket: ", err)
			return err
		}
		return nil
	})
	if err != nil {
		log.Fatal("failed to update database: ", err)
		return nil, err
	}

	srv := &server{
		numMsgRetries: make(map[string]int),
		msgQueue:      make(chan *internal.Message),
		reportedMsgs:  make(map[string]struct{}),
		db:            db,
		sentMsgs:      make(map[string]struct{}),
	}

	go srv.processMessages()

	return srv, nil
}

func (srv *server) processMessages() {
	for msg := range srv.msgQueue {
		log.Printf("processing message %s", msg.Id)

		if len(srv.clients) > 0 {
			for count := 0; count < 3; count++ {
				if len(srv.clients) == 0 {
					time.Sleep(3 * time.Second)
					continue
				}

				srv.sentMsgsMtx.Lock()

				srv.numMsgRetries[msg.Id]++

				if _, ok := srv.sentMsgs[msg.Id]; !ok {
					client := srv.clients[rand.Intn(len(srv.clients))]

					log.Printf("sending message %s to client %d", msg.Id, client.Id)

					select {
					case client.MsgChan <- msg:
						srv.sentMsgs[msg.Id] = struct{}{}
					case <-client.disconnect:
						srv.clients = removeClient(srv.clients, client.Id)
						srv.sentMsgsMtx.Unlock()
						continue
					default:
						log.Printf("failed to send message %s to client %d", msg.Id, client.Id)
					}
				} else if count, ok := srv.numMsgRetries[msg.Id]; ok && count == 3 {
					log.Printf("ignore message %s", msg.Id)
					srv.storeMessage(msg)
				} else {
					log.Printf("max retries reached for message %s", msg.Id)
					count = 3
				}

				srv.sentMsgsMtx.Unlock()

			}
		} else {
			time.Sleep(3 * time.Second)
		}
	}
}

func (srv *server) storeMessage(msg *internal.Message) error {
	err := srv.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("log"))
		if bucket == nil {
			log.Fatal("failed to find database bucket")
		}

		encoded, err := json.Marshal(msg)
		if err != nil {
			log.Fatal("failed to unmarshal message: ", err)
		}

		err = bucket.Put([]byte(msg.Id), encoded)
		if err != nil {
			log.Fatal("failed to put message into database bucket: ", err)
		}

		return nil
	})

	if err != nil {
		log.Fatal("failed to update database: ", err)
		return err
	}

	return nil
}

func (srv *server) sendMessages(w http.ResponseWriter, r *http.Request) {
	batchSize := 1
	if q := r.URL.Query().Get("batchsize"); q != "" {
		fmt.Sscanf(q, "%d", &batchSize)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, _ := w.(http.Flusher)

	msgChan := make(chan *internal.Message)

	client := &client{
		Id:         len(srv.clients),
		MsgChan:    msgChan,
		disconnect: make(chan bool),
	}

	if c, ok := w.(http.CloseNotifier); ok {
		closeNotify := c.CloseNotify()
		go func() {
			if <-closeNotify {
				client.disconnect <- true
			}
		}()
	}

	srv.clients = append(srv.clients, client)

	defer func() {
		client.disconnect <- true

		srv.clients = removeClient(srv.clients, client.Id)
		close(client.MsgChan)
	}()

	for msg := range msgChan {
		data, err := json.Marshal(msg)
		if err != nil {
			log.Fatal("failed to marshal message: ", err)
			continue
		}

		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
}

func (srv *server) reportMessage(w http.ResponseWriter, r *http.Request) {
	log.Printf("received report")

	var id string
	err := json.NewDecoder(r.Body).Decode(&id)
	if err != nil {
		http.Error(w, "Failed to decode report", http.StatusBadRequest)
	}

	srv.reportedMsgsMtx.Lock()

	srv.reportedMsgs[id] = struct{}{}
	delete(srv.numMsgRetries, id)

	srv.reportedMsgsMtx.Unlock()

	w.WriteHeader(http.StatusOK)
}

func removeClient(clients []*client, clientId int) []*client {
	defer log.Printf("removed client %d", clientId)

	result := []*client{}
	for _, client := range clients {
		if client.Id != clientId {
			result = append(result, client)
		}
	}

	return result
}
