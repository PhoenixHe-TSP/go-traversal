package main

import (
	"github.com/urfave/cli"
	"github.com/growse/concurrent-expiring-map"
	"time"
	"math/rand"
	"net"
	"log"
	"encoding/binary"
	gt "../common"
	"os"
)

func main() {
	rand.Seed(int64(time.Now().Nanosecond()))
	
	myApp := cli.NewApp()
	myApp.Name = "go-traversal-relay"
	myApp.Flags = []cli.Flag{
		cli.StringFlag{
			Name:	"listen,l",
			Value: ":8007",
			Usage: "local listen address",
		},
	}
	
	myApp.Action = func(c *cli.Context) error {
		listenAddr, err := net.ResolveUDPAddr("udp4", c.String("listen"));
		gt.CheckError(err)
		listener, err := net.ListenUDP("udp", listenAddr)
		gt.CheckError(err)

		cache := cmap.New()
		buf := make([]byte, 1500)
		
		for {
			size, remoteAddr, err := listener.ReadFromUDP(buf)
			if err != nil {
				log.Printf("ReadFromUDP: %+v\n", err)
				continue
			}

			data, sessionId, typeId, err := gt.ParseMessage(buf[:size])
			if err != nil {
				log.Printf("ParseMessage: %+v\n", err)
				continue
			}
			if sessionId != 0 {
				log.Printf("ParseMessage: Wrong session id: %d\n", sessionId)
				continue
			}
			if len(data) < 4 {
				log.Print("ParseMessage: Message too short\n")
				continue
			}
			targetId := binary.LittleEndian.Uint32(data[:4])

			switch typeId {
			case gt.TYPE_QUERY:
				targetAddr, found := cache.Get(string(targetId))
				if !found {
					log.Printf("%s request %d but not found\n", remoteAddr.String(), targetId)
					continue
				}
				
				ip := (net.IP)(targetAddr[:len(targetAddr) - 2])
				log.Printf("%s request %d, return %s:%d\n",
					remoteAddr.String(), targetId,
					ip.String(), binary.LittleEndian.Uint16(targetAddr[len(targetAddr) - 2:]))
				
				size := gt.MakeMessage(buf, 0, gt.TYPE_QUERY_ANSWER, targetAddr)
				_, err := listener.WriteToUDP(buf[:size], remoteAddr)
				if err != nil {
					log.Printf("Write UDP: %+v\n", err)
				}

			case gt.TYPE_SERVER_KEEP_ALIVE:
				targetAddr := make([]byte, len(remoteAddr.IP) + 2)
				copy(targetAddr, remoteAddr.IP)
				binary.LittleEndian.PutUint16(targetAddr[len(remoteAddr.IP):], uint16(remoteAddr.Port))
				cache.Set(string(targetId), targetAddr, time.Now().Add(60 * time.Second))
				log.Printf("%d server keep alive, addr: %s\n", targetId, remoteAddr.String())
			
			default:
				log.Printf("ParseMessage: Wrong type id: %d\n", targetId)
			}
		}
	}

	myApp.Run(os.Args)
}
