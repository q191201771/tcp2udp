package main

import (
	"encoding/binary"
	"log"
)

var TCP_ADDR = ":10014"
var UDP_ADDR = ":10014"
var TCP_READ_TIMEOUT_SEC = 10
var UDP_READ_TIMEOUT_SEC = 10

var TYPE_APP = uint8(204)

var (
	SUBTYPE_PUBLISH = uint8(1)
	SUBTYPE_PLAY    = uint8(2)
)

func TCPFilter(pair *ConnPair, packet []byte) bool {
	if len(packet) < 12 {
		return true
	}

	if packet[1] == TYPE_APP {
		subtype := packet[0] & 0x1F
		senderSsrc := binary.BigEndian.Uint32(packet[4:])
		mediaSsrc := binary.BigEndian.Uint32(packet[8:])
		if subtype == SUBTYPE_PUBLISH {
			log.Printf("[INF] %p TCP recvd app publish. sender ssrc: %v, media ssrc: %v", pair, senderSsrc, mediaSsrc)
		} else if subtype == SUBTYPE_PLAY {
			log.Printf("[INF] %p TCP recvd app play. sender ssrc: %v, media ssrc: %v", pair, senderSsrc, mediaSsrc)
		}
	}

	return true
}

func UDPFilter(pair *ConnPair, packet []byte) bool {
	return true
}

func main() {
	t2u := NewTCP2UDPWithDefaultConfig()
	t2u.SetTimeout(10, 10)
	t2u.SetFilter(TCPFilter, UDPFilter)
	t2u.Run(TCP_ADDR, UDP_ADDR)
}
