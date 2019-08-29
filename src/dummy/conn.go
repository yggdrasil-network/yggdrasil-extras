package dummy

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"github.com/Arceliar/phony"
	"github.com/yggdrasil-network/yggdrasil-go/src/address"
	"github.com/yggdrasil-network/yggdrasil-go/src/util"
	"github.com/yggdrasil-network/yggdrasil-go/src/yggdrasil"
)

const dummyConnTimeout = 2 * time.Minute

type dummyConn struct {
	phony.Inbox
	dummy *DummyAdapter
	conn  *yggdrasil.Conn
	addr  address.Address
	snet  address.Subnet
	stop  chan struct{}
	alive *time.Timer // From calling time.AfterFunc
}

func (s *dummyConn) close() {
	s.dummy.Act(s, s._close_from_dummy)
}

func (s *dummyConn) _close_from_dummy() {
	s.conn.Close()
	delete(s.dummy.addrToConn, s.addr)
	delete(s.dummy.subnetToConn, s.snet)
	func() {
		defer func() { recover() }()
		close(s.stop) // Closes reader/writer goroutines
	}()
}

func (s *dummyConn) _read(bs []byte) (err error) {
	select {
	case <-s.stop:
		err = errors.New("session was already closed")
		util.PutBytes(bs)
		return
	default:
	}
	if len(bs) == 0 {
		err = errors.New("read packet with 0 size")
		util.PutBytes(bs)
		return
	}
	// ipv4 := len(bs) > 20 && bs[0]&0xf0 == 0x40
	ipv6 := len(bs) > 40 && bs[0]&0xf0 == 0x60
	isCGA := true
	// Check source addresses
	switch {
	case ipv6 && bs[8] == 0x02 && bytes.Equal(s.addr[:16], bs[8:24]): // source
	case ipv6 && bs[8] == 0x03 && bytes.Equal(s.snet[:8], bs[8:16]): // source
	default:
		isCGA = false
	}
	// Check destiantion addresses
	switch {
	case ipv6 && bs[24] == 0x02 && bytes.Equal(s.dummy.addr[:16], bs[24:40]): // destination
	case ipv6 && bs[24] == 0x03 && bytes.Equal(s.dummy.subnet[:8], bs[24:32]): // destination
	default:
		isCGA = false
	}
	// Decide how to handle the packet
	var skip bool
	switch {
	case isCGA: // Allowed
	default:
		skip = true
	}
	if skip {
		err = errors.New("address not allowed")
		util.PutBytes(bs)
		return
	}
	s.dummy.writer.writeFrom(s, bs)
	s.stillAlive()
	return
}

func (s *dummyConn) writeFrom(from phony.Actor, bs []byte) {
	s.Act(from, func() {
		s._write(bs)
	})
}

func (s *dummyConn) _write(bs []byte) (err error) {
	select {
	case <-s.stop:
		err = errors.New("session was already closed")
		util.PutBytes(bs)
		return
	default:
	}
	// v4 := len(bs) > 20 && bs[0]&0xf0 == 0x40
	v6 := len(bs) > 40 && bs[0]&0xf0 == 0x60
	isCGA := true
	// Check source addresses
	switch {
	case v6 && bs[8] == 0x02 && bytes.Equal(s.dummy.addr[:16], bs[8:24]): // source
	case v6 && bs[8] == 0x03 && bytes.Equal(s.dummy.subnet[:8], bs[8:16]): // source
	default:
		isCGA = false
	}
	// Check destiantion addresses
	switch {
	case v6 && bs[24] == 0x02 && bytes.Equal(s.addr[:16], bs[24:40]): // destination
	case v6 && bs[24] == 0x03 && bytes.Equal(s.snet[:8], bs[24:32]): // destination
	default:
		isCGA = false
	}
	// Decide how to handle the packet
	var skip bool
	switch {
	case isCGA: // Allowed
	default:
		skip = true
	}
	if skip {
		err = errors.New("address not allowed")
		util.PutBytes(bs)
		return
	}
	msg := yggdrasil.FlowKeyMessage{
		FlowKey: util.GetFlowKey(bs),
		Message: bs,
	}
	s.conn.WriteFrom(s, msg, func(err error) {
		if err == nil {
			// No point in wasting resources to send back an error if there was none
			return
		}
		s.Act(s.conn, func() {
			if e, eok := err.(yggdrasil.ConnError); !eok {
				if e.Closed() {
					s.dummy.log.Debugln(s.conn.String(), "TUN/TAP generic write debug:", err)
				} else {
					s.dummy.log.Errorln(s.conn.String(), "TUN/TAP generic write error:", err)
				}
			} else if e.PacketTooBig() {
				// TODO: This currently isn't aware of IPv4 for CKR
				/*
					ptb := &icmp.PacketTooBig{
						MTU:  int(e.PacketMaximumSize()),
						Data: bs[:900],
					}
					if packet, err := CreateICMPv6(bs[8:24], bs[24:40], ipv6.ICMPTypePacketTooBig, 0, ptb); err == nil {
						s.dummy.writer.writeFrom(s, packet)
					}
				*/
				fmt.Println("PACKET TOO BIG! TODO: Do something useful here")
			} else {
				if e.Closed() {
					s.dummy.log.Debugln(s.conn.String(), "TUN/TAP conn write debug:", err)
				} else {
					s.dummy.log.Errorln(s.conn.String(), "TUN/TAP conn write error:", err)
				}
			}
		})
	})
	s.stillAlive()
	return
}

func (s *dummyConn) stillAlive() {
	if s.alive != nil {
		s.alive.Stop()
	}
	s.alive = time.AfterFunc(dummyConnTimeout, s.close)
}
