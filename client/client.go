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
)

func clientForward(forwardListener *net.UDPConn, serverConn *net.UDPConn) {
	buf := make([]byte, 1500)
	headerSize := gt.MakeMessage(buf, globalSessionId, gt.TYPE_DATA, nil)
	recvBuf := buf[headerSize:]
	
	for {
		size, remoteAddr, err := forwardListener.ReadFromUDP(recvBuf)
		if err != nil {
			log.Printf("ClientForwardRecv: %v+\n", err)
			continue
		}
		
		atomic.StorePointer(&localAddrPtr, unsafe.Pointer(remoteAddr))
		
		serverAddr := (*net.UDPAddr)(atomic.LoadPointer(&serverAddrPtr))

		_, err = serverConn.WriteToUDP(buf[:headerSize + size], serverAddr)
		if err != nil {
			log.Printf("DATA: Send to server: %v+\n", err)
		}
	}
}

func clientMainRecv(relayConn *net.UDPConn, targetId uint32, localSendConn *net.UDPConn) {
	buf := make([]byte, 1500)

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
			ip := make([]byte, len(data) - 2)
			copy(ip, data)
			if err != nil {
				log.Print("QUERY_ANSWER: Cannot parse IP\n")
				continue
			}
			port := binary.LittleEndian.Uint16(data[len(data) - 2:])
			addr := &net.UDPAddr{ip, int(port), ""}
			
			atomic.StorePointer(&serverAddrPtr, unsafe.Pointer(addr))
			log.Printf("Got query answer, server %d has addr %s\n", targetId, addr.String())

		case gt.TYPE_DATA:
			if sessionId != globalSessionId {
				log.Print("DATA: Wrong sessionId\n")
				continue
			}
			// TODO: notify relay if this address changed?
			atomic.StorePointer(&serverAddrPtr, unsafe.Pointer(remoteAddr))
			
			localAddr := (*net.UDPAddr)(atomic.LoadPointer(&localAddrPtr))

			_, err := localSendConn.WriteToUDP(data, localAddr)
			if err != nil {
				log.Printf("DATA: Write to local: %v+\n", err)
			}

		case gt.TYPE_SERVER_KEEP_ALIVE:
		
		default:
			log.Printf("ParseMessage: Unknown typeId: %d\n", typeId)
		}
	}
}

func clientQuerySend(relayConn *net.UDPConn, relayAddr *net.UDPAddr, targetId int) {
	query := make([]byte, 1500)
	t := make([]byte, 4)
	binary.LittleEndian.PutUint32(t, uint32(targetId))
	size := gt.MakeMessage(query, 0, gt.TYPE_QUERY, t)
	query = query[:size]
	
	for {
		_, err := relayConn.WriteToUDP(query, relayAddr)
		if err != nil {
			log.Printf("ClientQuerySend: %v+\n", err)
		}
		
		timeout := 10
		target := (*net.UDPAddr)(atomic.LoadPointer(&serverAddrPtr))
		if target == nil {
			timeout = 2
		}
		time.Sleep(time.Duration(timeout) * time.Second)
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
		
		targetId := c.Int("server-id")
		globalSessionId = rand.Uint32()
		
		go clientQuerySend(listener, relayAddr, targetId)
		go clientForward(forwardListener, listener)
		clientMainRecv(listener, uint32(targetId), forwardListener)
		
		return nil
	}

	myApp.Run(os.Args)
}
