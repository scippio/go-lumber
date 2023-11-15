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
	"crypto/tls"
	"encoding/json"
	"errors"
	"time"

	"github.com/scippio/go-lumber/lj"
)

// Option type for configuring server run options.
type Option func(*options) error

type options struct {
	timeout   time.Duration
	keepalive time.Duration
	decoder   jsonDecoder
	tls       *tls.Config
	v1        bool
	v2        bool
	ch        chan *lj.Batch
	logging   bool
}

type jsonDecoder func([]byte, interface{}) error

// Keepalive configures the keepalive interval returning an ACK of length 0 to
// lumberjack client, notifying clients the batch being still active.
func Keepalive(kl time.Duration) Option {
	return func(opt *options) error {
		if kl < 0 {
			return errors.New("keepalive must not be negative")
		}
		opt.keepalive = kl
		return nil
	}
}

// Timeout configures server network timeouts.
func Timeout(to time.Duration) Option {
	return func(opt *options) error {
		if to < 0 {
			return errors.New("timeouts must not be negative")
		}
		opt.timeout = to
		return nil
	}
}

// TLS enables and configures TLS support in lumberjack server.
func TLS(tls *tls.Config) Option {
	return func(opt *options) error {
		opt.tls = tls
		return nil
	}
}

// Channel option is used to register custom channel received batches will be
// forwarded to.
func Channel(c chan *lj.Batch) Option {
	return func(opt *options) error {
		opt.ch = c
		return nil
	}
}

// JSONDecoder sets an alternative json decoder for parsing events if protocol
// version 2 is enabled. The default is json.Unmarshal.
func JSONDecoder(decoder func([]byte, interface{}) error) Option {
	return func(opt *options) error {
		opt.decoder = decoder
		return nil
	}
}

// V1 enables lumberjack protocol version 1.
func V1(b bool) Option {
	return func(opt *options) error {
		opt.v1 = b
		return nil
	}
}

// V2 enables lumberjack protocol version 2.
func V2(b bool) Option {
	return func(opt *options) error {
		opt.v2 = b
		return nil
	}
}

func Logging(b bool) Option {
	return func(opt *options) error {
		opt.logging = b
		return nil
	}
}

func applyOptions(opts []Option) (options, error) {
	o := options{
		decoder:   json.Unmarshal,
		timeout:   30 * time.Second,
		keepalive: 3 * time.Second,
		v1:        true,
		v2:        true,
		tls:       nil,
		logging:   true,
	}

	for _, opt := range opts {
		if err := opt(&o); err != nil {
			return o, err
		}
	}
	return o, nil
}
