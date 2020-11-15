package dummy

// This manages the dummy driver to send/recv packets to/from applications

import (
	"encoding/hex"
	"errors"

	"github.com/Arceliar/phony"
	"github.com/gologme/log"

	"github.com/yggdrasil-network/yggdrasil-go/src/address"
	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/crypto"
	"github.com/yggdrasil-network/yggdrasil-go/src/defaults"
	"github.com/yggdrasil-network/yggdrasil-go/src/yggdrasil"
)

const dummy_IPv6_HEADER_LENGTH = 40

// DummyAdapter represents a running Dummy interface and extends the
// yggdrasil.Adapter type. In order to use the Dummy adapter with Yggdrasil,
// you should pass this object to the yggdrasil.SetRouterAdapter() function
// before calling yggdrasil.Start().
type DummyAdapter struct {
	writer       dummyWriter
	reader       dummyReader
	config       *config.NodeState
	log          *log.Logger
	reconfigure  chan chan error
	listener     *yggdrasil.Listener
	dialer       *yggdrasil.Dialer
	addr         address.Address
	subnet       address.Subnet
	mtu          int
	Conduit      *Conduit
	phony.Inbox  // Currently only used for _handlePacket from the reader, TODO: all the stuff that currently needs a mutex below
	addrToConn   map[address.Address]*dummyConn
	subnetToConn map[address.Subnet]*dummyConn
	dials        map[crypto.NodeID][][]byte // Buffer of packets to send after dialing finishes
	isOpen       bool
}

// Gets the maximum supported MTU for the platform based on the defaults in
// defaults.GetDefaults().
func getSupportedMTU(mtu int) int {
	if mtu > int(defaults.GetDefaults().MaximumIfMTU) {
		return int(defaults.GetDefaults().MaximumIfMTU)
	}
	return mtu
}

// Name returns the name of the adapter, e.g. "dummy0". On Windows, this may
// return a canonical adapter name instead.
func (dummy *DummyAdapter) Name() string {
	return "dummy"
}

// MTU gets the adapter's MTU. This can range between 1280 and 65535, although
// the maximum value is determined by your platform. The returned value will
// never exceed that of MaximumMTU().
func (dummy *DummyAdapter) MTU() int {
	return getSupportedMTU(dummy.mtu)
}

// IsTAP returns true if the adapter is a TAP adapter (Layer 2) or false if it
// is a TUN adapter (Layer 3).
func (dummy *DummyAdapter) IsTAP() bool {
	return false
}

// DefaultName gets the default Dummy interface name for your platform.
func DefaultName() string {
	return "dummy"
}

// DefaultMTU gets the default Dummy interface MTU for your platform. This can
// be as high as MaximumMTU(), depending on platform, but is never lower than 1280.
func DefaultMTU() int {
	return int(defaults.GetDefaults().DefaultIfMTU)
}

// DefaultIsTAP returns true if the default adapter mode for the current
// platform is TAP (Layer 2) and returns false for TUN (Layer 3).
func DefaultIsTAP() bool {
	return false
}

// MaximumMTU returns the maximum supported Dummy interface MTU for your
// platform. This can be as high as 65535, depending on platform, but is never
// lower than 1280.
func MaximumMTU() int {
	return int(defaults.GetDefaults().MaximumIfMTU)
}

// Init initialises the Dummy module. You must have acquired a Listener from
// the Yggdrasil core before this point and it must not be in use elsewhere.
func (dummy *DummyAdapter) Init(config *config.NodeState, log *log.Logger, listener *yggdrasil.Listener, dialer *yggdrasil.Dialer) {
	dummy.config = config
	dummy.log = log
	dummy.listener = listener
	dummy.dialer = dialer
	dummy.addrToConn = make(map[address.Address]*dummyConn)
	dummy.subnetToConn = make(map[address.Subnet]*dummyConn)
	dummy.dials = make(map[crypto.NodeID][][]byte)
	dummy.writer.dummy = dummy
	dummy.reader.dummy = dummy
}

// Start the setup process for the Dummy adapter. If successful, starts the
// reader actor to handle packets on that interface.
func (dummy *DummyAdapter) Start() error {
	var err error
	phony.Block(dummy, func() {
		err = dummy._start()
	})
	return err
}

func (dummy *DummyAdapter) _start() error {
	current := dummy.config.GetCurrent()
	if dummy.config == nil || dummy.listener == nil || dummy.dialer == nil {
		return errors.New("No configuration available to Dummy")
	}
	var boxPub crypto.BoxPubKey
	boxPubHex, err := hex.DecodeString(current.EncryptionPublicKey)
	if err != nil {
		return err
	}
	copy(boxPub[:], boxPubHex)
	nodeID := crypto.GetNodeID(&boxPub)
	dummy.addr = *address.AddrForNodeID(nodeID)
	dummy.subnet = *address.SubnetForNodeID(nodeID)
	dummy.mtu = int(current.IfMTU)
	dummy.isOpen = true
	dummy.reconfigure = make(chan chan error)
	go func() {
		for {
			e := <-dummy.reconfigure
			e <- nil
		}
	}()
	go dummy.handler()                        // nolint:errcheck
	dummy.reader.Act(nil, dummy.reader._read) // Start the reader
	/*
			  dummy.icmpv6.Init(dummy)
				if iftapmode {
					go dummy.icmpv6.Solicit(dummy.addr)
				}
		dummy.ckr.init(dummy)
	*/
	return nil
}

// Start the setup process for the Dummy adapter. If successful, starts the
// read/write goroutines to handle packets on that interface.
func (dummy *DummyAdapter) Stop() error {
	var err error
	phony.Block(dummy, func() {
		err = dummy._stop()
	})
	return err
}

func (dummy *DummyAdapter) _stop() error {
	dummy.isOpen = false
	// TODO: we have nothing that cleanly stops all the various goroutines opened
	// by Dummy, e.g. readers/writers, sessions
	dummy.Conduit.Close()
	return nil
}

// UpdateConfig updates the Dummy module with the provided config.NodeConfig
// and then signals the various module goroutines to reconfigure themselves if
// needed.
func (dummy *DummyAdapter) UpdateConfig(config *config.NodeConfig) {
	dummy.log.Debugln("Reloading Dummy configuration...")

	// Replace the active configuration with the supplied one
	dummy.config.Replace(*config)
}

func (dummy *DummyAdapter) handler() error {
	for {
		// Accept the incoming connection
		conn, err := dummy.listener.Accept()
		if err != nil {
			dummy.log.Errorln("Dummy connection accept error:", err)
			return err
		}
		yconn, ok := conn.(*yggdrasil.Conn)
		if !ok {
			err = errors.New("handler: failed type assertion")
			dummy.log.Errorln("Dummy connection accept error:", err)
			return err
		}
		phony.Block(dummy, func() {
			if _, err := dummy._wrap(yconn); err != nil {
				// Something went wrong when storing the connection, typically that
				// something already exists for this address or subnet
				dummy.log.Debugln("Dummy handler wrap:", err)
			}
		})
	}
}

func (dummy *DummyAdapter) _wrap(conn *yggdrasil.Conn) (c *dummyConn, err error) {
	// Prepare a session wrapper for the given connection
	s := dummyConn{
		dummy: dummy,
		conn:  conn,
		stop:  make(chan struct{}),
	}
	c = &s
	// Get the remote address and subnet of the other side
	remotePubKey, ok := conn.RemoteAddr().(*crypto.BoxPubKey)
	if !ok {
		err = errors.New("_wrap: failed type assertion")
		return
	}
	remoteNodeID := crypto.GetNodeID(remotePubKey)
	s.addr = *address.AddrForNodeID(remoteNodeID)
	s.snet = *address.SubnetForNodeID(remoteNodeID)
	// Work out if this is already a destination we already know about
	atc, aok := dummy.addrToConn[s.addr]
	stc, sok := dummy.subnetToConn[s.snet]
	// If we know about a connection for this destination already then assume it
	// is no longer valid and close it
	if aok {
		atc._close_from_dummy()
		err = errors.New("replaced connection for address")
	} else if sok {
		stc._close_from_dummy()
		err = errors.New("replaced connection for subnet")
	}
	// Save the session wrapper so that we can look it up quickly next time
	// we receive a packet through the interface for this address
	dummy.addrToConn[s.addr] = &s
	dummy.subnetToConn[s.snet] = &s
	// Set the read callback and start the timeout
	conn.SetReadCallback(func(bs []byte) {
		s.Act(conn, func() {
			_ = s._read(bs)
		})
	})
	s.Act(nil, s.stillAlive)
	// Return
	return c, err
}
