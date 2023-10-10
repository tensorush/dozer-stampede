## Specification

### Goal

Implement a **client-server architecture** with test coverage.

### Message

```go
type msg struct {
    Id string // https://github.com/rs/xid
    Period uint64 // Random from 1 to 1000
}
```

### Server

1) `/task` endpoint accepts the `batchsize` query parameter and sends that number of `msg`s to idle clients.
2) `/report` endpoint accepts an array of `msg.Id`s and marks them as completed in `???` without sending them again.
3) In case a `msg` has been sent, but the client's `report` doesn't contain it, the server performs up to 3 retries.
4) If all retries failed, the server logs the respective `msg.Id` to `bbolt`.
5) Every `msg` is handled by a single client.

### Client

1) Randomly generates `batchsize` in range from 2 to 10, inclusively.
2) Connects via SSE to the server's `/task` endpoint.
3) Spawns a goroutine for every received `msg` and puts it to sleep for `msg.Period` milliseconds.
4) In case a `msg` is received when `batchsize` number of goroutines are already running, a client should log an error.
5) If the `msg.Period > 800` the goroutine panics, which the server handles without shutting down.
6) If the `msg.Period > 900` the server shuts down.
7) Once all the goroutines join, a client posts the array of `msg.Id` to the server's `/report` endpoint.
