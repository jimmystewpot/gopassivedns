package main

import (
	"errors"
	"net"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

//  struct to store either reassembled TCP streams or packets
// Type will be tcp or packet for those type
// or it can be 'flush' or 'stop' to signal packet handling threads
// codebeat:disable[TOO_MANY_IVARS]
type packetData struct {
	packet   gopacket.Packet
	tcpdata  TCPDataStruct
	datatype string

	foundLayerTypes []gopacket.LayerType

	ethLayer  *layers.Ethernet
	IPv4Layer *layers.IPv4
	IPv6Layer *layers.IPv6
	udpLayer  *layers.UDP
	tcpLayer  *layers.TCP
	dns       *layers.DNS
	payload   *gopacket.Payload
}

// codebeat:enable[TOO_MANY_IVARS]

func newTCPData(tcpdata TCPDataStruct) *packetData {
	var pd packetData
	pd.datatype = "tcp"
	pd.tcpdata = tcpdata
	return &pd
}

func newPacketData(packet gopacket.Packet) *packetData {
	var pd packetData
	pd.datatype = "packet"
	pd.packet = packet
	return &pd
}

func (pd *packetData) Parse() error {

	if pd.datatype == "tcp" {
		pd.dns = &layers.DNS{}
		pd.payload = &gopacket.Payload{}
		//for parsing the reassembled TCP streams
		dnsParser := gopacket.NewDecodingLayerParser(
			layers.LayerTypeDNS,
			pd.dns,
			pd.payload,
		)

		dnsParser.DecodeLayers(pd.tcpdata.DNSData, &pd.foundLayerTypes)

		return nil
	} else if pd.datatype == "packet" {
		pd.ethLayer = &layers.Ethernet{}
		pd.IPv4Layer = &layers.IPv4{}
		pd.IPv6Layer = &layers.IPv6{}
		pd.udpLayer = &layers.UDP{}
		pd.tcpLayer = &layers.TCP{}
		pd.dns = &layers.DNS{}
		pd.payload = &gopacket.Payload{}
		//we're constraining the set of layer decoders that gopacket will apply
		//to this traffic. this MASSIVELY speeds up the parsing phase
		parser := gopacket.NewDecodingLayerParser(
			layers.LayerTypeEthernet,
			pd.ethLayer,
			pd.IPv4Layer,
			pd.IPv6Layer,
			pd.udpLayer,
			pd.tcpLayer,
			pd.dns,
			pd.payload,
		)

		parser.DecodeLayers(pd.packet.Data(), &pd.foundLayerTypes)

		return nil

	} else {
		return errors.New("Bad packet type: " + pd.datatype)
	}
}

func (pd *packetData) GetSrcIP() net.IP {
	if pd.IPv4Layer != nil {
		return pd.IPv4Layer.SrcIP
	}
	if pd.IPv6Layer != nil {
		return pd.IPv6Layer.SrcIP
	}
	return net.IP(pd.tcpdata.IPLayer.Src().Raw())
}

func (pd *packetData) GetDstIP() net.IP {
	if pd.IPv4Layer != nil {
		return pd.IPv4Layer.DstIP
	}
	if pd.IPv6Layer != nil {
		return pd.IPv6Layer.DstIP
	}
	return net.IP(pd.tcpdata.IPLayer.Dst().Raw())
}

func (pd *packetData) IsTCPStream() bool {
	return pd.datatype == "tcp"
}

func (pd *packetData) GetTCPLayer() *layers.TCP {
	return pd.tcpLayer
}

func (pd *packetData) GetIPv4Layer() *layers.IPv4 {
	return pd.IPv4Layer
}

func (pd *packetData) GetIPv6Layer() *layers.IPv6 {
	return pd.IPv6Layer
}

func (pd *packetData) GetDNSLayer() *layers.DNS {
	return pd.dns
}

func (pd *packetData) HasTCPLayer() bool {
	return foundLayerType(layers.LayerTypeTCP, pd.foundLayerTypes)
}

func (pd *packetData) HasIPv4Layer() bool {
	return foundLayerType(layers.LayerTypeIPv4, pd.foundLayerTypes)
}

func (pd *packetData) HasIPv6Layer() bool {
	return foundLayerType(layers.LayerTypeIPv6, pd.foundLayerTypes)
}

func (pd *packetData) HasDNSLayer() bool {
	return foundLayerType(layers.LayerTypeDNS, pd.foundLayerTypes)
}

func (pd *packetData) GetTimestamp() *time.Time {
	if pd.datatype == "packet" {
		return &pd.packet.Metadata().Timestamp
	}
	return nil

}

func (pd *packetData) GetSize() *int {
	if pd.datatype == "packet" {
		return &pd.packet.Metadata().Length
	}
	// This needs to be improved. Currently because GetSize only works with UDP
	// that is because we can't measure the size of the entire re-assembled stream
	// of TCP right now. Fix pending.
	sz := int(0)
	return &sz
}

func (pd *packetData) GetProto() *string {
	return &pd.datatype
}
