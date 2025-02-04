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

package v2

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"time"

	"github.com/klauspost/compress/zlib"

	"github.com/scippio/go-lumber/lj"
	"github.com/scippio/go-lumber/log"
	protocol "github.com/scippio/go-lumber/protocol/v2"
)

type reader struct {
	conn       net.Conn
	in         *bufio.Reader
	tlsState   *tls.ConnectionState
	decoder    jsonDecoder
	remoteAddr string
	buf        []byte
	timeout    time.Duration
}

type jsonDecoder func([]byte, interface{}) error

func newReader(c net.Conn, to time.Duration, jsonDecoder jsonDecoder) *reader {
	r := &reader{
		conn:       c,
		in:         bufio.NewReader(c),
		decoder:    jsonDecoder,
		remoteAddr: c.RemoteAddr().String(),
		buf:        make([]byte, 0, 64),
		timeout:    to,
	}

	if tlsConn, ok := c.(*tls.Conn); ok {
		s := tlsConn.ConnectionState()
		r.tlsState = &s
	}
	return r
}

func (r *reader) ReadBatch() (*lj.Batch, error) {
	// 1. read window size
	var win [6]byte
	_ = r.conn.SetReadDeadline(time.Time{}) // wait for next batch without timeout
	if err := readFull(r.in, win[:]); err != nil {
		return nil, err
	}

	if win[0] != protocol.CodeVersion && win[1] != protocol.CodeWindowSize {
		log.Printf("Expected window from. Received %v", win[0:1])
		return nil, ErrProtocolError
	}

	count := int(binary.BigEndian.Uint32(win[2:]))
	if count == 0 {
		return nil, nil
	}

	if err := r.conn.SetReadDeadline(time.Now().Add(r.timeout)); err != nil {
		return nil, err
	}

	events, err := r.readEvents(r.in, make([]interface{}, 0, count))
	if events == nil || err != nil {
		log.Printf("readEvents failed with: %v", err)
		return nil, err
	}

	return lj.NewBatchWithSourceMetadata(events, r.remoteAddr, r.tlsState), nil
}

func (r *reader) readEvents(in io.Reader, events []interface{}) ([]interface{}, error) {
	for len(events) < cap(events) {
		var hdr [2]byte
		if err := readFull(in, hdr[:]); err != nil {
			return nil, err
		}

		if hdr[0] != protocol.CodeVersion {
			log.Println("Event protocol version error")
			return nil, ErrProtocolError
		}

		switch hdr[1] {
		case protocol.CodeJSONDataFrame:
			event, err := r.readJSONEvent(in)
			if err != nil {
				log.Printf("failed to read json event with: %v\n", err)
				return nil, err
			}
			events = append(events, event)
		case protocol.CodeCompressed:
			readEvents, err := r.readCompressed(in, events)
			if err != nil {
				return nil, err
			}
			events = readEvents
		default:
			log.Printf("Unknown frame type: %v", hdr[1])
			return nil, ErrProtocolError
		}
	}
	return events, nil
}

func (r *reader) readJSONEvent(in io.Reader) (interface{}, error) {
	var hdr [8]byte
	if err := readFull(in, hdr[:]); err != nil {
		return nil, err
	}

	payloadSz := int(binary.BigEndian.Uint32(hdr[4:]))
	if payloadSz > len(r.buf) {
		r.buf = make([]byte, payloadSz)
	}

	buf := r.buf[:payloadSz]
	if err := readFull(in, buf); err != nil {
		return nil, err
	}

	var event interface{}
	err := r.decoder(buf, &event)
	return event, err
}

func (r *reader) readCompressed(in io.Reader, events []interface{}) ([]interface{}, error) {
	var hdr [4]byte
	if err := readFull(in, hdr[:]); err != nil {
		return nil, err
	}

	payloadSz := binary.BigEndian.Uint32(hdr[:])
	limit := io.LimitReader(in, int64(payloadSz))
	reader, err := zlib.NewReader(limit)
	if err != nil {
		log.Printf("Failed to initialized zlib reader %v\n", err)
		return nil, err
	}

	events, err = r.readEvents(reader, events)
	if err != nil {
		_ = reader.Close()
		return nil, err
	}
	if err := reader.Close(); err != nil {
		return nil, err
	}

	// consume final bytes from limit reader
	for {
		var tmp [16]byte
		if _, err := limit.Read(tmp[:]); err != nil {
			if err != io.EOF {
				return nil, err
			}
			break
		}
	}
	return events, nil
}

func readFull(in io.Reader, buf []byte) error {
	_, err := io.ReadFull(in, buf)
	return err
}
