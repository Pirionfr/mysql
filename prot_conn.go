/*
  The MIT License (MIT)

  Copyright (c) 2015 Nirbhay Choubey

  Permission is hereby granted, free of charge, to any person obtaining a copy
  of this software and associated documentation files (the "Software"), to deal
  in the Software without restriction, including without limitation the rights
  to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
  copies of the Software, and to permit persons to whom the Software is
  furnished to do so, subject to the following conditions:

  The above copyright notice and this permission notice shall be included in all
  copies or substantial portions of the Software.

  THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
  IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
  FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
  AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
  LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
  OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
  SOFTWARE.
*/

package mysql

import (
	"net"
)

const (
	_INITIAL_PACKET_BUFFER_SIZE = 4 * 1024 //  4KB
)

type Conn struct {
	// connection properties
	p properties

	conn  net.Conn
	buff  buffer
	rw    readWriter
	seqno uint8 // packet sequence number

	// OK packet
	affectedRows uint64
	lastInsertId uint64
	statusFlags  uint16
	warnings     uint16

	// ERR packet
	e Error

	// handshake initialization packet (from server)
	serverVersion      string
	connectionId       uint32
	serverCapabilities uint32
	serverCharset      uint8
	authPluginData     []byte
	authPluginName     string

	// handshake response packet (from client)
	clientCharset uint8
}

func open(p properties) (*Conn, error) {
	var err error

	c := &Conn{}
	c.rw = &defaultReadWriter{}
	c.p = p

	// initialize the connection buffer
	c.buff.New(_INITIAL_PACKET_BUFFER_SIZE)

	// open a connection with the server
	if c.conn, err = dial(p.address, p.socket); err != nil {
		return nil, err
	} else {
		c.rw.init(c)
	}

	// perform handshake
	if err = c.handshake(); err != nil {
		return nil, err
	}

	return c, nil
}

// readPacket reads the next available protocol packet from the network into
// the connection buffer. It also increments the packet sequence number.
func (c *Conn) readPacket() ([]byte, error) {
	var (
		err           error
		b             []byte
		payloadLength int
	)

	// first read the packet header

	// reset the connection buffer
	if b, err = c.buff.Reset(4); err != nil {
		return nil, err
	}
	if _, err = c.rw.read(b, 4); err != nil {
		return nil, err
	}

	// payload length
	payloadLength = int(getUint24(b[0:3]))

	// error out in case the packet is too big.
	// if compression is enabled, we check it in readCompressedPacket().
	if c.p.clientCapabilities&_CLIENT_COMPRESS == 0 &&
		payloadLength+4 > int(c.p.maxPacketSize) {
		return nil, myError(ErrNetPacketTooLarge)
	}

	// check for out-of-order packets
	if c.seqno != b[3] {
		return nil, myError(ErrNetPacketsOutOfOrder)
	}

	// increment the packet sequence number
	c.seqno++

	// finally, read the payload (note: the header gets overwritten)

	// reset the connection buffer
	if b, err = c.buff.Reset(payloadLength); err != nil {
		return nil, err
	}
	if _, err = c.rw.read(b, payloadLength); err != nil {
		return nil, err
	}

	return b[0:payloadLength], nil
}

// writePacket populates the specified packet buffer with header and writes it
// to the network.
func (c *Conn) writePacket(b []byte) error {
	var (
		err           error
		payloadLength int
	)

	payloadLength = len(b) - 4

	// populate the packet header
	putUint24(b[0:3], uint32(payloadLength)) // payload length
	b[3] = c.seqno                           // packet sequence number

	// write it to the connection
	if _, err = c.rw.write(b); err != nil {
		return err
	}

	// finally, increment the packet sequence number
	c.seqno++

	return nil
}

// resetSeqno resets the packet sequence number.
func (c *Conn) resetSeqno() {
	c.seqno = 0
	c.rw.reset()
}
