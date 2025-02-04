// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package internal

import (
	"net"
	"sync"
	"time"

	"github.com/scippio/go-lumber/lj"
	"github.com/scippio/go-lumber/log"
)

type defaultHandler struct {
	cb        Eventer
	client    net.Conn
	reader    BatchReader
	writer    ACKWriter
	keepalive time.Duration
	logging   bool

	signal chan struct{}
	ch     chan *lj.Batch

	stopGuard sync.Once
}

type BatchReader interface {
	ReadBatch() (*lj.Batch, error)
}

type ACKWriter interface {
	Keepalive(int) error
	ACK(int) error
}

type ProtocolFactory func(conn net.Conn) (BatchReader, ACKWriter, error)

func DefaultHandler(
	keepalive time.Duration,
	mk ProtocolFactory,
	logging bool,
) HandlerFactory {
	return func(cb Eventer, client net.Conn) (Handler, error) {
		r, w, err := mk(client)
		if err != nil {
			return nil, err
		}

		return &defaultHandler{
			cb:        cb,
			client:    client,
			reader:    r,
			writer:    w,
			keepalive: keepalive,
			signal:    make(chan struct{}),
			ch:        make(chan *lj.Batch),
			logging:   logging,
		}, nil
	}
}

func (h *defaultHandler) Run() {
	// start async routine for returning ACKs to client.
	// Sends ACK of 0 every 'keepalive' seconds to signal
	// client the batch still being in pipeline
	go h.ackLoop()
	if err := h.handle(); err != nil {
		log.Println(err)
	}
}

func (h *defaultHandler) Stop() {
	h.stopGuard.Do(func() {
		close(h.signal)
		_ = h.client.Close()
	})
}

func (h *defaultHandler) handle() error {
	if h.logging {
		log.Printf("Start client handler")
		defer log.Printf("client handler stopped")
	}
	defer close(h.ch)
	defer h.Stop()

	for {
		// 1. read data into batch
		b, err := h.reader.ReadBatch()
		if err != nil {
			return err
		}

		// read next batch if empty batch has been received
		if b == nil {
			continue
		}

		// 2. push batch to ACK queue
		select {
		case <-h.signal:
			return nil
		case h.ch <- b:
		}

		// 3. push batch to server receive queue:
		if err := h.cb.OnEvents(b); err != nil {
			return nil
		}
	}
}

func (h *defaultHandler) ackLoop() {
	if h.logging {
		log.Println("start client ack loop")
		defer log.Println("client ack loop stopped")
	}

	// drain queue on shutdown.
	// Stop ACKing batches in case of error, forcing client to reconnect
	defer func() {
		log.Println("drain ack loop")
		//nolint:revive // This drains the channel.
		for range h.ch {
		}
	}()

	for {
		select {
		case <-h.signal: // return on client/server shutdown
			if h.logging {
				log.Println("receive client connection close signal")
			}
			return
		case b, open := <-h.ch:
			if !open {
				return
			}
			if err := h.waitACK(b); err != nil {
				return
			}
		}
	}
}

func (h *defaultHandler) waitACK(batch *lj.Batch) error {
	n := len(batch.Events)

	if h.keepalive <= 0 {
		for {
			select {
			case <-h.signal:
				return nil
			case <-batch.Await():
				// send ack
				return h.writer.ACK(n)
			}
		}
	} else {
		for {
			select {
			case <-h.signal:
				return nil
			case <-batch.Await():
				// send ack
				return h.writer.ACK(n)
			case <-time.After(h.keepalive):
				if err := h.writer.Keepalive(0); err != nil {
					return err
				}
			}
		}
	}
}
