package mock

import (
	"github.com/consideritdone/landslidecore/libs/log"
	"github.com/consideritdone/landslidecore/p2p"
	"github.com/consideritdone/landslidecore/p2p/conn"
)

type Reactor struct {
	p2p.BaseReactor

	Channels []*conn.ChannelDescriptor
}

func NewReactor() *Reactor {
	r := &Reactor{}
	r.BaseReactor = *p2p.NewBaseReactor("Mock-PEX", r)
	r.SetLogger(log.TestingLogger())
	return r
}

func (r *Reactor) GetChannels() []*conn.ChannelDescriptor            { return r.Channels }
func (r *Reactor) AddPeer(peer p2p.Peer)                             {}
func (r *Reactor) RemovePeer(peer p2p.Peer, reason interface{})      {}
func (r *Reactor) Receive(chID byte, peer p2p.Peer, msgBytes []byte) {}
