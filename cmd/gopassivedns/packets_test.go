package main

import (
	"testing"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

func BenchmarkPacketParse(b *testing.B) {
	var ethLayer layers.Ethernet
	var IPv4Layer layers.IPv4
	var IPv6Layer layers.IPv6
	parser := gopacket.NewDecodingLayerParser(
		layers.LayerTypeEthernet,
		&ethLayer,
		&IPv4Layer,
		&IPv6Layer,
	)
	packetSource := getPacketData("100_udp_lookups")
	packetSource.DecodeOptions.Lazy = true
	packetSource.DecodeOptions.NoCopy = true

	for i := 0; i < b.N; i++ {

		foundLayerTypes := []gopacket.LayerType{}
		for packet := range packetSource.Packets() {
			parser.DecodeLayers(packet.Data(), &foundLayerTypes)
			if foundLayerType(layers.LayerTypeIPv4, foundLayerTypes) {
				pd := newPacketData(packet)
				err := pd.Parse()
				if err != nil {
					b.Errorf("got err %s on %s", err, packet)
				}
			}
		}
	}
}
