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
	"crypto/tls"
	"io"
	"net"
	"sync"

	"github.com/scippio/go-lumber/lj"
	"github.com/scippio/go-lumber/log"
)

type Server struct {
	listener net.Listener
	opts     Config
	ch       chan *lj.Batch
	ownCH    bool
	sig      closeSignaler
}

type Config struct {
	TLS     *tls.Config
	Handler HandlerFactory
	Channel chan *lj.Batch
}

type Handler interface {
	Run()
	Stop()
}

type HandlerFactory func(Eventer, net.Conn) (Handler, error)

type Eventer interface {
	OnEvents(*lj.Batch) error
}

type chanCallback struct {
	done <-chan struct{}
	ch   chan *lj.Batch
}

func newChanCallback(done <-chan struct{}, ch chan *lj.Batch) *chanCallback {
	return &chanCallback{done, ch}
}

func (c *chanCallback) OnEvents(b *lj.Batch) error {
	select {
	case <-c.done:
		return io.EOF
	case c.ch <- b:
		return nil
	}
}

func NewWithListener(l net.Listener, opts Config) (*Server, error) {
	s := &Server{
		listener: l,
		sig:      makeCloseSignaler(),
		ch:       opts.Channel,
		opts:     opts,
	}

	if s.ch == nil {
		s.ownCH = true
		s.ch = make(chan *lj.Batch, 128)
	}

	s.sig.Add(1)
	go s.run()

	return s, nil
}

func ListenAndServeWith(
	binder func(network, addr string) (net.Listener, error),
	addr string,
	opts Config,
) (*Server, error) {
	l, err := binder("tcp", addr)
	if err != nil {
		return nil, err
	}
	return NewWithListener(l, opts)
}

func ListenAndServe(addr string, opts Config) (*Server, error) {
	binder := net.Listen
	if opts.TLS != nil {
		binder = func(network, addr string) (net.Listener, error) {
			return tls.Listen(network, addr, opts.TLS)
		}
	}

	return ListenAndServeWith(binder, addr, opts)
}

func (s *Server) Close() error {
	err := s.listener.Close()
	s.sig.Close()
	if s.ownCH {
		close(s.ch)
	}
	return err
}

func (s *Server) Receive() *lj.Batch {
	select {
	case <-s.sig.Sig():
		return nil
	case b := <-s.ch:
		return b
	}
}

func (s *Server) ReceiveChan() <-chan *lj.Batch {
	return s.ch
}

func (s *Server) run() {
	defer s.sig.Done()

	for {
		client, err := s.listener.Accept()
		if err != nil {
			break
		}

		log.Printf("New connection from %v", client.RemoteAddr())
		s.startConnHandler(client)
	}
}

func (s *Server) startConnHandler(client net.Conn) {
	var wgStart sync.WaitGroup

	h, err := s.opts.Handler(newChanCallback(s.sig.Sig(), s.ch), client)
	if err != nil {
		log.Printf("Failed to initialize client handler: %v", h)
		return
	}

	s.sig.Add(1)
	wgStart.Add(1)
	stopped := make(chan struct{}, 1)
	go func() {
		defer s.sig.Done()
		defer close(stopped) // signal handler loop stopped

		wgStart.Done()
		h.Run()
	}()

	wgStart.Wait()
	go func() {
		select {
		case <-s.sig.Sig():
			// server shutdown
			h.Stop()

		case <-stopped:
			// handler loop stopped
		}
	}()
}
