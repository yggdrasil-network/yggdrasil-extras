package mobile

import (
	"encoding/json"
	"fmt"
	"time"
	"os"
	"github.com/gologme/log"

	hjson "github.com/hjson/hjson-go"
	"github.com/mitchellh/mapstructure"
	"github.com/vikulin/yggdrasil-extras/src/dummy"
	"github.com/yggdrasil-network/yggdrasil-go/src/admin"
	"github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/multicast"
	"github.com/yggdrasil-network/yggdrasil-go/src/yggdrasil"
)

// Yggdrasil mobile package is meant to "plug the gap" for mobile support, as
// Gomobile will not create headers for Swift/Obj-C etc if they have complex
// (non-native) types. Therefore for iOS we will expose some nice simple
// functions. Note that in the case of iOS we handle reading/writing to/from TUN
// in Swift therefore we use the "dummy" TUN interface instead.
type Yggdrasil struct {
	core      yggdrasil.Core
	state     config.NodeState
	admin     admin.AdminSocket
	multicast multicast.Multicast
	dummy     dummy.DummyAdapter
	log       MobileLogger
}

func (m *Yggdrasil) addStaticPeers(cfg *config.NodeConfig) {
	if len(cfg.Peers) == 0 && len(cfg.InterfacePeers) == 0 {
		return
	}
	for {
		for _, peer := range cfg.Peers {
			m.core.AddPeer(peer, "")
			time.Sleep(time.Second)
		}
		for intf, intfpeers := range cfg.InterfacePeers {
			for _, peer := range intfpeers {
				m.core.AddPeer(peer, intf)
				time.Sleep(time.Second)
			}
		}
		time.Sleep(time.Minute)
	}
}

// StartAutoconfigure starts a node with a randomly generated config
func (m *Yggdrasil) StartAutoconfigure() (*dummy.ConduitEndpoint, error) {
	return m.StartJSON([]byte("{}"))
}

// StartJSON starts a node with the given JSON config. You can get JSON config
// (rather than HJSON) by using the GenerateConfigJSON() function
func (m *Yggdrasil) StartJSON(configjson []byte) (conduit *dummy.ConduitEndpoint, err error) {
	logger := log.New(m.log, "", 0)
	logger.EnableLevel("error")
	logger.EnableLevel("warn")
	logger.EnableLevel("info")
	logger.EnableLevel("debug")
	logger.EnableLevel("trace")
	m.state.Current = *config.GenerateConfig()
	var dat map[string]interface{}
	if err := hjson.Unmarshal(configjson, &dat); err != nil {
		return nil, err
	}
	if err := mapstructure.Decode(dat, &m.state.Current); err != nil {
		return nil, err
	}
	m.state.Current.IfName = "dummy"
	m.state.Previous = m.state.Current
	// Start Yggdrasil
	state, err := m.core.Start(&m.state.Current, logger)
	if err != nil {
		logger.Errorln("An error occured starting Yggdrasil:", err)
		return nil, err
	}
	// Start the admin socket
	m.admin.Init(&m.core, state, logger, nil)
	if err := m.admin.Start(); err != nil {
		logger.Errorln("An error occurred starting admin socket:", err)
	}
	// Start the multicast module
	m.multicast.Init(&m.core, state, logger, nil)
	if err := m.multicast.Start(); err != nil {
		logger.Errorln("An error occurred starting multicast:", err)
	}
	// Create the conduit for the dummy interface
	m.dummy.Conduit = dummy.CreateConduit()
	conduit = dummy.CreateConduitEndpoint(m.dummy.Conduit)
	// Start the dummy interface
	if listener, err := m.core.ConnListen(); err == nil {
		if dialer, err := m.core.ConnDialer(); err == nil {
			m.dummy.Init(&m.state, logger, listener, dialer)
			if err := m.dummy.Start(); err != nil {
				logger.Errorln("An error occurred starting dummy:", err)
			}
		} else {
			logger.Errorln("Unable to get Dialer:", err)
		}
	} else {
		logger.Errorln("Unable to get Listener:", err)
	}
	go m.addStaticPeers(&m.state.Current)
	return
}

// Stop the mobile Yggdrasil instance
func (m *Yggdrasil) Stop() {
	//m.admin.Stop()
	//m.core.Stop()
	logger.Infoln("exitting...1")
	m.Stop()
	logger.Infoln("exitting...2")
	os.Exit(0)
	logger.Infoln("exit done")
}

// GenerateConfigJSON generates mobile-friendly configuration in JSON format
func GenerateConfigJSON() []byte {
	nc := config.GenerateConfig()
	nc.IfName = "dummy"
	if json, err := json.Marshal(nc); err == nil {
		return json
	}
	return nil
}

// GetAddressString gets the node's IPv6 address
func (m *Yggdrasil) GetAddressString() string {
	ip := m.core.Address()
	return ip.String()
}

// GetSubnetString gets the node's IPv6 subnet in CIDR notation
func (m *Yggdrasil) GetSubnetString() string {
	subnet := m.core.Subnet()
	return subnet.String()
}

// GetBoxPubKeyString gets the node's public encryption key
func (m *Yggdrasil) GetBoxPubKeyString() string {
	return m.core.EncryptionPublicKey()
}

// GetSigPubKeyString gets the node's public signing key
func (m *Yggdrasil) GetSigPubKeyString() string {
	return m.core.SigningPublicKey()
}

func (m *Yggdrasil) GetCoordsString() string {
	return fmt.Sprintf("%v", m.core.Coords())
}

func (m *Yggdrasil) GetPeersJSON() (result string) {
	if res, err := json.Marshal(m.core.GetPeers()); err == nil {
		return string(res)
	} else {
		return "{}"
	}
}

func (m *Yggdrasil) GetSwitchPeersJSON() string {
	if res, err := json.Marshal(m.core.GetSwitchPeers()); err == nil {
		return string(res)
	} else {
		return "{}"
	}
}
