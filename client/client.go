package main

import (
	"github.com/urfave/cli"
	"time"
	"math/rand"
	"net"
	"log"
	"encoding/binary"
	"sync/atomic"
	"unsafe"
	gt "../common"
	"os"
)

var (
	// atomic
	serverAddrPtr, localAddrPtr unsafe.Pointer
	globalSessionId			 uint32
	die chan int
)

func clientForward(forwardListener *net.UDPConn, serverConn *net.UDPConn) {
	packet := make([]byte, 1500)
	size := gt.MakeMessage(packet, globalSessionId, gt.TYPE_KEEP_ALIVE, nil)
	keepAlivePacket := make([]byte, size)
	copy(keepAlivePacket, packet)
	
	headerSize := gt.MakeMessage(packet, globalSessionId, gt.TYPE_DATA, nil)
	buf := packet[headerSize:]
	
	for {
		forwardListener.SetReadDeadline(time.Now().Add(10 * time.Second))
		size, remoteAddr, err := forwardListener.ReadFromUDP(buf)
		
		serverAddr := (*net.UDPAddr)(atomic.LoadPointer(&serverAddrPtr))
		atomic.StorePointer(&localAddrPtr, unsafe.Pointer(remoteAddr))
		if err != nil {
			if opError, ok := err.(*net.OpError); ok && opError.Timeout() {
				// Read timeout, send keep alive packet
				serverConn.WriteToUDP(keepAlivePacket, serverAddr)
				continue
			}
			log.Printf("ClientForwardRecv: %v+\n", err)
			continue
		}
		
		serverConn.WriteToUDP(packet[:headerSize + size], serverAddr)
	}
}

func clientMainRecv(relayConn *net.UDPConn, targetId uint32, localSendConn *net.UDPConn, input chan int) {
	buf := make([]byte, 1500)
	connected := false

	for {
		size, remoteAddr, err := relayConn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("ClientMainRecv: %v+\n", err)
			continue
		}
		
		data, sessionId, typeId, err := gt.ParseMessage(buf[:size])
		if err != nil {
			if err != nil {
				log.Printf("ParseMessage: %+v\n", err)
				continue
			}
		}
		
		switch typeId {
		case gt.TYPE_QUERY_ANSWER:
			if sessionId != 0 {
				log.Printf("ParseMessage: Wrong session id: %d\n", sessionId)
				continue
			}
			if len(data) < 10 {
				log.Print("QUERY_ANWSER: Packet too short\n")
				continue
			}
			addr := gt.BytesToUDPAddr(data)
			
			atomic.StorePointer(&serverAddrPtr, unsafe.Pointer(addr))
			log.Printf("Got query answer, server %d has addr %s\n",
				targetId, addr.String())

		case gt.TYPE_DATA, gt.TYPE_KEEP_ALIVE:
			if sessionId != globalSessionId {
				log.Printf("DATA: Wrong sessionId %d\n", sessionId)
				continue
			}
			input <- 0
			// TODO: notify relay if this address changed?
			atomic.StorePointer(&serverAddrPtr, unsafe.Pointer(remoteAddr))
			
			if typeId == gt.TYPE_DATA {
				localAddr := (*net.UDPAddr)(atomic.LoadPointer(&localAddrPtr))
				
				_, err := localSendConn.WriteToUDP(data, localAddr)
				if err != nil {
					log.Printf("DATA: Write to local: %v+\n", err)
				}
			}
			if !connected {
				log.Printf("Connected to server %d, remote addr %s\n",
					targetId, remoteAddr.String())
				connected = true
			}

		default:
			log.Printf("ParseMessage: Unknown typeId: %d\n", typeId)
		}
	}
}

func clientCheckTimeout(input chan int) {
	for {
		t := time.NewTimer(25 * time.Second)
		select {
		case <-input:
			t.Stop()
		case <-t.C:
			log.Print("No data from server in 25 seconds. Reconnecting...")
			atomic.StorePointer(&serverAddrPtr, unsafe.Pointer(nil))
			die <- 0
		}
	}
}

func clientQuerySend(relayConn *net.UDPConn, relayAddr *net.UDPAddr, targetId uint32, reverseConnect bool) {
	query := make([]byte, 1500)
	if reverseConnect {
		t := make([]byte, 8)
		binary.LittleEndian.PutUint32(t[:4], targetId)
		binary.LittleEndian.PutUint32(t[4:], globalSessionId)
		size := gt.MakeMessage(query, 0, gt.TYPE_REVERSE_CONNECT, t)
		query = query[:size]
	} else {
		t := make([]byte, 4)
		binary.LittleEndian.PutUint32(t, targetId)
		size := gt.MakeMessage(query, 0, gt.TYPE_QUERY, t)
		query = query[:size]
	}
	
	for {
		_, err := relayConn.WriteToUDP(query, relayAddr)
		if err != nil {
			log.Printf("ClientQuerySend: %v+\n", err)
		}
		
		timeout := 20
		target := (*net.UDPAddr)(atomic.LoadPointer(&serverAddrPtr))
		if target == nil {
			timeout = 2
		}
		t := time.After(time.Duration(timeout) * time.Second)
		select {
		case <-t:
		case <-die:
		}
	}
}

func main() {
	rand.Seed(int64(time.Now().Nanosecond()))
	
	myApp := cli.NewApp()
	myApp.Name = "go-traversal-client"
	myApp.Flags = []cli.Flag{
		cli.StringFlag{
			Name:	"listen,l",
			Value: ":8008",
			Usage: "local listen address",
		},
		cli.StringFlag{
			Name:	"forward,f",
			Value: ":8001",
			Usage: "local forward address",
		},
		cli.StringFlag{
			Name:	"relay,r",
			Value: "127.0.0.1:8007",
			Usage: "relay server address",
		},
		cli.StringFlag{
			Name:	"server-id,i",
			Value: "7",
			Usage: "target server id",
		},
		cli.StringFlag{
			Name: "reverse-connect,R",
			Usage: "ask server to connect client",
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
		forwardListener, err := net.ListenUDP("udp", forwardAddr)
		gt.CheckError(err)
		
		targetId := uint32(c.Int("server-id"))
		reverseConnect := c.Bool("reverse-connect")
		globalSessionId = rand.Uint32()
		
		go clientQuerySend(listener, relayAddr, targetId, reverseConnect)
		go clientForward(forwardListener, listener)
		input := make(chan int)
		die = make(chan int)
		go clientCheckTimeout(input)
		clientMainRecv(listener, uint32(targetId), forwardListener, input)
		
		return nil
	}

	myApp.Run(os.Args)
}
