package dummy

import (
	"context"
	"errors"

	"github.com/yggdrasil-network/yggdrasil-go/src/address"
	"github.com/yggdrasil-network/yggdrasil-go/src/crypto"
	"github.com/yggdrasil-network/yggdrasil-go/src/yggdrasil"

	"github.com/Arceliar/phony"
)

type dummyWriter struct {
	phony.Inbox
	dummy *DummyAdapter
}

func (w *dummyWriter) writeFrom(from phony.Actor, b []byte) {
	w.Act(from, func() {
		w._write(b)
	})
}

// write is pretty loose with the memory safety rules, e.g. it assumes it can read w.dummy.Conduit.IsTap() safely
func (w *dummyWriter) _write(b []byte) {
	var written int
	var err error
	n := len(b)
	if n == 0 {
		return
	}
	written, err = w.dummy.Conduit.Write(b[:n])
	//util.PutBytes(b)
	if err != nil {
		w.dummy.Act(w, func() {
			if !w.dummy.isOpen {
				w.dummy.log.Errorln("Dummy iface write error:", err)
			}
		})
	}
	if written != n {
		w.dummy.log.Errorln("Dummy iface write mismatch:", written, "bytes written vs", n, "bytes given")
	}
}

type dummyReader struct {
	phony.Inbox
	dummy *DummyAdapter
}

func (r *dummyReader) _read() {
	// Get a slice to store the packet in
	recvd := [65535]byte{}
	// Wait for a packet to be delivered to us through the Dummy adapter
	n, err := r.dummy.Conduit.Read(recvd[:])
	if n != 0 {
		r.dummy.handlePacketFrom(r, recvd[:n], err)
	}
	if err == nil {
		// Now read again
		r.Act(nil, r._read)
	}
}

func (dummy *DummyAdapter) handlePacketFrom(from phony.Actor, packet []byte, err error) {
	dummy.Act(from, func() {
		dummy._handlePacket(packet, err)
	})
}

// does the work of reading a packet and sending it to the correct dummyConn
func (dummy *DummyAdapter) _handlePacket(bs []byte, err error) {
	if err != nil {
		dummy.log.Errorln("Dummy iface read error:", err)
		return
	}
	// From the IP header, work out what our source and destination addresses
	// and node IDs are. We will need these in order to work out where to send
	// the packet
	var dstAddr address.Address
	var dstSnet address.Subnet
	var addrlen int
	n := len(bs)
	// Check the IP protocol - if it doesn't match then we drop the packet and
	// do nothing with it
	if bs[0]&0xf0 == 0x60 {
		// Check if we have a fully-sized IPv6 header
		if len(bs) < 40 {
			return
		}
		// Check the packet size
		if n-dummy_IPv6_HEADER_LENGTH != 256*int(bs[4])+int(bs[5]) {
			return
		}
		// IPv6 address
		addrlen = 16
		copy(dstAddr[:addrlen], bs[24:])
		copy(dstSnet[:addrlen/2], bs[24:])
	} else if bs[0]&0xf0 == 0x40 {
		// Check if we have a fully-sized IPv4 header
		if len(bs) < 20 {
			return
		}
		// Check the packet size
		if n != 256*int(bs[2])+int(bs[3]) {
			return
		}
		// IPv4 address
		addrlen = 4
		copy(dstAddr[:addrlen], bs[16:])
	} else {
		// Unknown address length or protocol, so drop the packet and ignore it
		dummy.log.Traceln("Unknown packet type, dropping")
		dummy.log.Traceln("received:", bs)
		return
	}
	if addrlen != 16 || (!dstAddr.IsValid() && !dstSnet.IsValid()) {
		// Couldn't find this node's ygg IP
		return
	}
	// Do we have an active connection for this node address?
	var dstNodeID, dstNodeIDMask *crypto.NodeID
	session, isIn := dummy.addrToConn[dstAddr]
	if !isIn || session == nil {
		session, isIn = dummy.subnetToConn[dstSnet]
		if !isIn || session == nil {
			// Neither an address nor a subnet mapping matched, therefore populate
			// the node ID and mask to commence a search
			if dstAddr.IsValid() {
				dstNodeID, dstNodeIDMask = dstAddr.GetNodeIDandMask()
			} else {
				dstNodeID, dstNodeIDMask = dstSnet.GetNodeIDandMask()
			}
		}
	}
	// If we don't have a connection then we should open one
	if !isIn || session == nil {
		// Check we haven't been given empty node ID, really this shouldn't ever
		// happen but just to be sure...
		if dstNodeID == nil || dstNodeIDMask == nil {
			panic("Given empty dstNodeID and dstNodeIDMask - this shouldn't happen")
		}
		_, known := dummy.dials[*dstNodeID]
		dummy.dials[*dstNodeID] = append(dummy.dials[*dstNodeID], bs)
		for len(dummy.dials[*dstNodeID]) > 32 {
			dummy.dials[*dstNodeID] = dummy.dials[*dstNodeID][1:]
		}
		if !known {
			go func() {
				conn, err := dummy.dialer.DialByNodeIDandMask(context.TODO(), dstNodeID, dstNodeIDMask)
				dummy.Act(nil, func() {
					packets := dummy.dials[*dstNodeID]
					delete(dummy.dials, *dstNodeID)
					if err != nil {
						return
					}
					// We've been given a connection so prepare the session wrapper
					var tc *dummyConn
					yconn, ok := conn.(*yggdrasil.Conn)
					if !ok {
						err = errors.New("_handlePacket: failed type assertion")
						return
					}
					if tc, err = dummy._wrap(yconn); err != nil {
						// Something went wrong when storing the connection, typically that
						// something already exists for this address or subnet
						dummy.log.Debugln("Dummy iface wrap:", err)
						return
					}
					for _, packet := range packets {
						tc.writeFrom(nil, packet)
					}
				})
			}()
		}
	}
	// If we have a connection now, try writing to it
	if isIn && session != nil {
		session.writeFrom(dummy, bs)
	}
}
