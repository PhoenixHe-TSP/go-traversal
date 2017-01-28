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
	input, die chan int
}

func serverKeepAlive(relayConn *net.UDPConn, relayAddr *net.UDPAddr, serverId uint32) {
	packet := make([]byte, 1500)
	t := make([]byte, 4)
	binary.LittleEndian.PutUint32(t, serverId)
	size := gt.MakeMessage(packet, 0, gt.TYPE_KEEP_ALIVE, t)
	packet = packet[:size]

	for {
		_, err := relayConn.WriteToUDP(packet, relayAddr)
		if err != nil {
			log.Printf("ServerKeepAlive: %v+\n", err)
		}

		time.Sleep(10 * time.Second)
	}
}

func serverForward(c *forwardConn, sessionId uint32) {
	packet := make([]byte, 1500)
	size := gt.MakeMessage(packet, sessionId, gt.TYPE_KEEP_ALIVE, nil)
	keepAlivePacket := make([]byte, size)
	copy(keepAlivePacket, packet)
	
	remoteAddr := (*net.UDPAddr)(atomic.LoadPointer(&c.remoteAddr))
	c.remoteConn.WriteToUDP(keepAlivePacket, remoteAddr)
	
	headerSize := gt.MakeMessage(packet, sessionId, gt.TYPE_DATA, nil)
	buf := packet[headerSize:]
	
	for {
		c.localConn.SetReadDeadline(time.Now().Add(10 * time.Second))
		size, err := c.localConn.Read(buf)
		
		remoteAddr := (*net.UDPAddr)(atomic.LoadPointer(&c.remoteAddr))
		
		select {
		case <- c.die:
			log.Printf("Session %d timeouts, remoteAddr %s\n", sessionId, remoteAddr.String())
			return
		default:
		}
		
		if err != nil {
			if opError, ok := err.(*net.OpError); ok && opError.Timeout() {
				// Read timeout, send keep alive packet
				c.remoteConn.WriteToUDP(keepAlivePacket, remoteAddr)
				continue
			}
			log.Printf("ServerForward: Read: %v+\n", err)
			continue
		}

		_, err = c.remoteConn.WriteToUDP(packet[:size + headerSize], remoteAddr)
		if err != nil {
			log.Printf("Forward write: %v+\n", err)
		}
	}
}

func serverCheckTimeout(c *forwardConn, sessionId uint32, timeout chan uint32) {
	for {
		t := time.NewTimer(60 * time.Second)
		select {
		case <-c.input:
			t.Stop()
		case <-t.C:
			c.die <- 0
			timeout <- sessionId
			return
		}
	}
}

func serverMainRecv(listener *net.UDPConn, forwardAddr *net.UDPAddr) {
	buf := make([]byte, 1500)
	clientConns := make(map[uint32]*forwardConn)
	timeouts := make(chan uint32, 16)

LOOP:
	for {
		listener.SetReadDeadline(time.Now().Add(10 * time.Second))
		size, remoteAddr, err := listener.ReadFromUDP(buf)
		if err != nil {
			if opError, ok := err.(*net.OpError); !ok || !opError.Timeout() {
				log.Printf("ServerMainRecv: %v+\n", err)
				continue
			}
		}
		
		// Clear all the timeouts
CLEAR_TIMEOUT:
		for {
			select {
			case sessionId := <-timeouts:
				c := clientConns[sessionId]
				c.localConn.Close()
				delete(clientConns, sessionId)
			default:
				break CLEAR_TIMEOUT
			}
		}
		
		data, sessionId, typeId, err := gt.ParseMessage(buf[:size])
		if err != nil {
			log.Printf("ParseMessage: %+v\n", err)
			continue
		}

		switch typeId {
		case gt.TYPE_DATA, gt.TYPE_KEEP_ALIVE:
			
		case gt.TYPE_REVERSE_CONNECT:
			sessionId = binary.LittleEndian.Uint32(data[:4])
			remoteAddr = gt.BytesToUDPAddr(data[4:])

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
			c = &forwardConn{conn, listener, unsafe.Pointer(remoteAddr), make(chan int), make(chan int)}
			clientConns[sessionId] = c
			log.Printf("Accept new session %d from %s\n", sessionId, remoteAddr.String())
			go serverForward(c, sessionId)
			go serverCheckTimeout(c, sessionId, timeouts)
			
		} else {
			c.input <- 0
			atomic.StorePointer(&c.remoteAddr, unsafe.Pointer(remoteAddr))
		}

		if typeId == gt.TYPE_DATA {
			_, err = c.localConn.Write(data)
			if err != nil {
				log.Printf("Local write: %v+\n", err)
			}
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
		listener, err := net.ListenUDP("udp4", listenAddr)
		gt.CheckError(err)

		relayAddr, err := net.ResolveUDPAddr("udp4", c.String("relay"))
		gt.CheckError(err)

		forwardAddr, err := net.ResolveUDPAddr("udp", c.String("forward"))
		gt.CheckError(err)

		serverId := uint32(c.Uint("server-id"))

		go serverKeepAlive(listener, relayAddr, serverId)
		serverMainRecv(listener, forwardAddr)

		return nil
	}

	myApp.Run(os.Args)
}
