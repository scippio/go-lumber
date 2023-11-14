# go-lumber
[![ci](https://github.com/scippio/go-lumber/actions/workflows/ci.yml/badge.svg)](https://github.com/scippio/go-lumber/actions/workflows/ci.yml)
[![Go Report
Card](https://goreportcard.com/badge/github.com/scippio/go-lumber)](https://goreportcard.com/report/github.com/scippio/go-lumber)
[![Contributors](https://img.shields.io/github/contributors/scippio/go-lumber.svg)](https://github.com/scippio/go-lumber/graphs/contributors)
[![GitHub release](https://img.shields.io/github/release/scippio/go-lumber.svg?label=changelog)](https://github.com/scippio/go-lumber/releases/latest)

Lumberjack protocol client and server implementations for go.

## Example Server

There is an example server in [cmd/tst-lj](cmd/tst-lj/main.go). It will accept
connections and log when it receives batches of events.

```
# Install to $GOPATH/bin.
go install github.com/scippio/go-lumber/cmd/tst-lj@latest

# Start server.
tst-lj -bind=localhost:5044 -v2
2022/08/14 00:13:54 Server config: server.options{timeout:30000000000, keepalive:3000000000, decoder:(server.jsonDecoder)(0x100d88e80), tls:(*tls.Config)(nil), v1:false, v2:true, ch:(chan *lj.Batch)(nil)}
2022/08/14 00:13:54 tcp server up
```
