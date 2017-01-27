package main

import (
	"github.com/urfave/cli"
	"net"
	gt "../common"
	"log"
	"time"
	"encoding/binary"
	"sync/atomic"
	"unsafe"
	"os"
)

type forwardConn struct {
	localConn, remoteConn *net.UDPConn
	remoteAddr unsafe.Pointer
}

func serverKeepAlive(relayConn *net.UDPConn, relayAddr *net.UDPAddr, serverId uint32) {
	packet := make([]byte, 1500)
	t := make([]byte, 4)
	binary.LittleEndian.PutUint32(t, serverId)
	size := gt.MakeMessage(packet, 0, gt.TYPE_SERVER_KEEP_ALIVE, t)
	packet = packet[:size]

	for {
		_, err := relayConn.WriteToUDP(packet, relayAddr)
		if err != nil {
			log.Printf("ServerKeepAlive: %v+\n", err)
		}

		timeout := 10
		time.Sleep(time.Duration(timeout) * time.Second)
	}
}

func serverForward(c *forwardConn, sessionId uint32, localAddr *net.UDPAddr) {
	packet := make([]byte, 1500)
	headerSize := gt.MakeMessage(packet, sessionId, gt.TYPE_DATA, nil)
	buf := packet[headerSize:]

	for {
		size, err := c.localConn.Read(buf)
		if err != nil {
			log.Printf("ServerForward: Read: %v+\n", err)
			continue
		}

		remoteAddr := (*net.UDPAddr)(atomic.LoadPointer(&c.remoteAddr))
		_, err = c.remoteConn.WriteToUDP(packet[:size + headerSize], remoteAddr)
		if err != nil {
			log.Printf("Forward write: %v+\n", err)
		}
	}
}

func serverMainRecv(listener *net.UDPConn,  localAddr, forwardAddr *net.UDPAddr) {
	buf := make([]byte, 1500)
	// TODO make client connection expired after timeout
	clientConns := make(map[uint32]*forwardConn)

LOOP:
	for {
		size, remoteAddr, err := listener.ReadFromUDP(buf)
		if err != nil {
			log.Printf("ServerMainRecv: %v+\n", err)
			continue
		}

		data, sessionId, typeId, err := gt.ParseMessage(buf[:size])
		if err != nil {
			log.Printf("ParseMessage: %+v\n", err)
			continue
		}

		switch typeId {
		case gt.TYPE_DATA, gt.TYPE_CLIENT_KEEP_ALIVE:

		default:
			log.Printf("ParseMessage: Unknown typeId: %d\n", typeId)
			continue LOOP
		}

		c, ok := clientConns[sessionId]
		if !ok {
			conn, err := net.DialUDP("udp", nil, forwardAddr)
			if err != nil {
				log.Printf("net.DialUDP: %v+\n", err)
				continue
			}
			c = &forwardConn{conn, listener, unsafe.Pointer(remoteAddr)}
			clientConns[sessionId] = c
			log.Printf("Accept new session %d from %s\n", sessionId, remoteAddr.String())
			go serverForward(c, sessionId, localAddr)
		} else {
			atomic.StorePointer(&c.remoteAddr, unsafe.Pointer(remoteAddr))
		}

		if typeId == gt.TYPE_CLIENT_KEEP_ALIVE {
			continue
		}

		_, err = c.localConn.Write(data)
		if err != nil {
			log.Printf("Local write: %v+\n", err)
		}
	}
}

func main() {
	myApp := cli.NewApp()
	myApp.Name = "go-traversal-client"
	myApp.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "listen,l",
			Value: ":8009",
			Usage: "local listen address",
		},
		cli.StringFlag{
			Name:  "forward,f",
			Value: ":8002",
			Usage: "local forward target address",
		},
		cli.StringFlag{
			Name:  "relay,r",
			Value: "127.0.0.1:8007",
			Usage: "relay server address",
		},
		cli.StringFlag{
			Name:  "server-id,i",
			Value: "7",
			Usage: "this server id",
		},
	}

	myApp.Action = func(c *cli.Context) error {
		listenAddr, err := net.ResolveUDPAddr("udp4", c.String("listen"))
		gt.CheckError(err)
		listener, err := net.ListenUDP("udp", listenAddr)
		gt.CheckError(err)

		relayAddr, err := net.ResolveUDPAddr("udp4", c.String("relay"))
		gt.CheckError(err)

		forwardAddr, err := net.ResolveUDPAddr("udp", c.String("forward"))
		gt.CheckError(err)

		serverId := uint32(c.Uint("server-id"))

		go serverKeepAlive(listener, relayAddr, serverId)
		serverMainRecv(listener, listenAddr, forwardAddr)

		return nil
	}

	myApp.Run(os.Args)
}
