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

package server

import (
	"errors"
	"io"
	"net"
	"sync"

	"github.com/scippio/go-lumber/lj"
	"github.com/scippio/go-lumber/log"
	v1 "github.com/scippio/go-lumber/server/v1"
	v2 "github.com/scippio/go-lumber/server/v2"
)

// Server serves multiple lumberjack clients.
type Server interface {
	// ReceiveChan returns a channel all received batch requests will be made
	// available on. Batches read from channel must be ACKed.
	ReceiveChan() <-chan *lj.Batch

	// Receive returns the next received batch from the receiver channel.
	// Batches returned by Receive must be ACKed.
	Receive() *lj.Batch

	// Close stops the listener, closes all active connections and closes the
	// receiver channel returned from ReceiveChan().
	Close() error

	Handle(conn net.Conn)
}

type server struct {
	ch    chan *lj.Batch
	ownCH bool

	done chan struct{}
	wg   sync.WaitGroup

	// netListener net.Listener
	mux []muxServer
}

type muxServer struct {
	mux    byte
	l      *muxListener
	server Server
}

// ErrNoVersionEnabled indicates no lumberjack protocol version being enabled
// when instantiating a server.
var ErrNoVersionEnabled = errors.New("no protocol version enabled")

// Close stops the listener, closes all active connections and closes the
// receiver channel returned from ReceiveChan()
func (s *server) Close() error {
	close(s.done)
	for _, m := range s.mux {
		m.server.Close()
	}
	// err := s.netListener.Close()
	s.wg.Wait()
	if s.ownCH {
		close(s.ch)
	}
	return nil
}

// ReceiveChan returns a channel all received batch requests will be made
// available on. Batches read from channel must be ACKed.
func (s *server) ReceiveChan() <-chan *lj.Batch {
	return s.ch
}

// Receive returns the next received batch from the receiver channel.
// Batches returned by Receive must be ACKed.
func (s *server) Receive() *lj.Batch {
	select {
	case <-s.done:
		return nil
	case b := <-s.ch:
		return b
	}
}

func NewServer(opts ...Option) (Server, error) {
	cfg, err := applyOptions(opts)
	if err != nil {
		return nil, err
	}

	var servers []func() (Server, byte, error)

	log.Printf("Server config: %#v", cfg)

	if cfg.v1 {
		servers = append(servers, func() (Server, byte, error) {
			s, err := v1.NewWithListener(nil,
				v1.Channel(cfg.ch),
				v1.TLS(cfg.tls))
			return s, '1', err
		})
	}
	if cfg.v2 {
		servers = append(servers, func() (Server, byte, error) {
			s, err := v2.NewWithListener(nil,
				v2.Timeout(cfg.timeout),
				v2.Channel(cfg.ch),
				v2.TLS(cfg.tls),
				v2.JSONDecoder(cfg.decoder))
			return s, '2', err
		})
	}

	if len(servers) == 0 {
		return nil, ErrNoVersionEnabled
	}
	if len(servers) == 1 {
		s, _, err := servers[0]()
		return s, err
	}

	ownCH := false
	if cfg.ch == nil {
		ownCH = true
		cfg.ch = make(chan *lj.Batch, 128)
	}

	mux := make([]muxServer, len(servers))
	for i, mk := range servers {
		// muxL := newMuxListener()
		log.Printf("mk: %v", i)
		s, b, err := mk()
		if err != nil {
			return nil, err
		}

		mux[i] = muxServer{
			mux: b,
			// l:      muxL,
			server: s,
		}
	}

	s := &server{
		ch:    cfg.ch,
		ownCH: ownCH,
		// netListener: l,
		mux:  mux,
		done: make(chan struct{}),
	}
	// s.wg.Add(1)

	return s, nil
}

// func (s *server) run() {
// 	defer s.wg.Done()
// 	for {
// 		client, err := s.netListener.Accept()
// 		if err != nil {
// 			break
// 		}

// 		s.Handle(client)
// 	}
// }

func (s *server) Handle(client net.Conn) {
	// read first byte and decide multiplexer

	sig := make(chan struct{})

	go func() {
		defer close(sig)

		var buf [1]byte
		if _, err := io.ReadFull(client, buf[:]); err != nil {
			client.Close()
			return
		}

		for _, m := range s.mux {
			if m.mux != buf[0] {
				continue
			}

			conn := newMuxConn(buf[0], client)
			m.l.ch <- conn
			return
		}
		client.Close()
	}()

	go func() {
		select {
		case <-sig:
		case <-s.done:
			// close connection if server being shut down
			client.Close()
		}
	}()
}
