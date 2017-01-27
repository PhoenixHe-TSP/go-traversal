package common

import (
	"encoding/binary"
	"errors"
	"net"
	"bytes"
	"log"
	"os"
)

const (
	TYPE_DATA              = 1
	TYPE_QUERY             = 2
	TYPE_QUERY_ANSWER      = 3
	TYPE_SERVER_KEEP_ALIVE = 4
	TYPE_CLIENT_KEEP_ALIVE = 5
)

func MakeMessage(packet []byte, sessionId uint32, typeId uint8, content []byte) (size int) {
	binary.LittleEndian.PutUint32(packet[:4], sessionId)
	packet = packet[4:]
	packet[0] = typeId
	packet = packet[1:]
	if content == nil {
		return 5
	}
	n := copy(packet, content)
	return n + 5
}

func ParseMessage(packet []byte) (data []byte, sessionId uint32, typeId uint8, err error) {
	if len(packet) < 5 {
		return nil, 0, 0, errors.New("Message too short")
	}

	sessionId = binary.LittleEndian.Uint32(packet[:4])
	packet = packet[4:]
	typeId = packet[0]
	data = packet[1:]

	return
}

func UdpAddrEqual(a, b *net.UDPAddr) bool {
	if a == nil || b == nil {
		return false
	}
	if !bytes.Equal(a.IP, b.IP) {
		return false
	}
	return a.Port == b.Port;
}

func CheckError(err error) {
	if err != nil {
		log.Printf("%+v\n", err)
		os.Exit(-1)
	}
}
