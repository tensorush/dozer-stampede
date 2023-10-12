package main

import (
	"bytes"
	"dozer-stampede/internal"
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/r3labs/sse/v2"
)

func main() {
	var wg sync.WaitGroup
	var workerCount int32
	var msgIdMutex = &sync.Mutex{}

	batchSize := rand.Intn(9) + 2
	eventChan := make(chan *sse.Event)
	seenMsgs := make(map[string]struct{})
	msgIds := make([]string, 0, batchSize)

	client := sse.NewClient("http://localhost:8080/task?batchsize=" + strconv.Itoa(batchSize))
	err := client.SubscribeChan("msgs", eventChan)
	if err != nil {
		log.Fatal("failed to subscribe to channel message id: ", err)
	}

	wg.Add(batchSize)
	go receiveMessage(eventChan, &wg, &workerCount, msgIdMutex, seenMsgs, msgIds, batchSize)
	wg.Wait()
}

func receiveMessage(eventChan chan *sse.Event, wg *sync.WaitGroup, workerCount *int32, msgIdMutex *sync.Mutex, seenMsgs map[string]struct{}, msgIds []string, batchSize int) {
	for event := range eventChan {
		var msg internal.Message
		if err := json.Unmarshal(event.Data, &msg); err != nil {
			log.Fatal("failed to unmarshal data: ", err)
		}

		log.Printf("received message %s", msg.Id)

		msgIdMutex.Lock()

		if _, ok := seenMsgs[msg.Id]; ok {
			log.Printf("skipped message %s", msg.Id)
			msgIdMutex.Unlock()
			continue
		}
		seenMsgs[msg.Id] = struct{}{}

		msgIdMutex.Unlock()

		go processMessage(&msg, wg, msgIdMutex, &msgIds, workerCount, batchSize)
	}
}

func processMessage(msg *internal.Message, wg *sync.WaitGroup, msgIdMutex *sync.Mutex, msgIds *[]string, workerCount *int32, batchSize int) {
	defer wg.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("recovered message %s: %v", msg.Id, recovered)
		}
	}()

	if atomic.AddInt32(workerCount, 1) > int32(batchSize) {
		log.Printf("ignored message %s", msg.Id)
		atomic.AddInt32(workerCount, -1)
		return
	}
	defer atomic.AddInt32(workerCount, -1)

	msgIdMutex.Lock()

	*msgIds = append(*msgIds, msg.Id)

	msgIdMutex.Unlock()

	if msg.Period > 900 {
		log.Printf("stopped message %s", msg.Id)
		os.Exit(1)
	} else if msg.Period > 800 {
		panic("message period > 800")
	}

	time.Sleep(time.Duration(msg.Period) * time.Millisecond)

	sendReport(msg.Id)
}

func sendReport(id string) {
	report, err := json.Marshal(id)
	if err != nil {
		log.Fatal("failed to marshal message id: ", err)
	}

	_, err = http.Post("http://localhost:8080/report", "application/json", bytes.NewBuffer(report))
	if err != nil {
		log.Fatal("failed to report message id: ", err)
	}
}
