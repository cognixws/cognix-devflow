# queue-page-data-race

## Next

## Debts

- [ ] queue page reads Run fields (queue.go ~299-304) without s.mu while execute writes them under lock (serve.go ~235); go test -race ./cmd/flow/TestQueuePageThroughTheRelay fails at base 5df2544, found while fixing the socket leak (0.10.1)
