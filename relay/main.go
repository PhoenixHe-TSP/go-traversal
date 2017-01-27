package main

import (
  "github.com/urfave/cli"
  "github.com/growse/concurrent-expiring-map"
  "time"
  "math/rand"
  "net"
  "log"
  "os"
  "errors"
  "encoding/binary"
)

func checkError(err error) {
  if err != nil {
    log.Printf("%+v\n", err)
    os.Exit(-1)
  }
}

const (
  TYPE_QUERY             = 1
  TYPE_QUERY_ANSWER      = 2
  TYPE_SERVER_KEEP_ALIVE = 3
)

func parseMessage(packet []byte) (sessionId uint32, typeId uint8, targetId uint32, err error) {
  if len(packet) < 9 {
    return nil, nil, nil, errors.New("Message too short")
  }
  
  sessionId = binary.LittleEndian.Uint32(packet[:4])
  packet = packet[4:]
  typeId = packet[0]
  packet = packet[1:]
  targetId = binary.LittleEndian.Uint32(packet[:4])
  
  return
}

func makeMessage(packet []byte, sessionId uint32, typeId uint8, content []byte) (size int) {
  binary.LittleEndian.PutUint32(packet[:4], sessionId)
  packet[0] = typeId
  packet = packet[1:]
  copy(packet, content)
  return len(content) + 5
}

func main() {
  rand.Seed(int64(time.Now().Nanosecond()))
  
  myApp := cli.NewApp()
  myApp.Name = "go-traversal-relay"
  myApp.Flags = []cli.Flag{
    cli.StringFlag{
      Name:  "listen,l",
      Value: ":8007",
      Usage: "local listen address",
    },
  }
  
  myApp.Action = func(c *cli.Context) error {
    listenAddr, err := net.ResolveUDPAddr("udp4", c.String("listen"));
    checkError(err)
    listener, err := net.ListenUDP("udp", listenAddr)
    checkError(err)
    
    cache := cmap.New()
    buf := make([]byte, 1500)
    
    for {
      size, remoteAddr, err := listener.ReadFromUDP(buf)
      if err != nil {
        log.Printf("ReadFromUDP: %+v\n", err)
        continue
      }
      
      sessionId, typeId, targetId, err := parseMessage(buf[:size])
      if err != nil {
        log.Printf("ParseMessage: %+v\n", err)
        continue
      }
      if sessionId != 0 {
        log.Printf("ParseMessage: Wrong session id: %d\n", sessionId)
        continue
      }
      
      switch typeId {
      case TYPE_QUERY:
        targetAddr, found := cache.Get(string(targetId))
        if !found {
          log.Printf("%s request %d but not found", remoteAddr.String(), targetId)
          continue
        }
        
        ip := (net.IP)(targetAddr[:len(targetAddr) - 2])
        log.Printf("%s request %d, return %s:%d",
          remoteAddr.String(), targetId,
          ip.String(), binary.LittleEndian.Uint16(targetAddr[len(targetAddr) - 2:]))
        
        makeMessage(buf, 0, TYPE_QUERY_ANSWER, targetAddr)
        _, err := listener.WriteToUDP(buf, remoteAddr)
        if err != nil {
          log.Printf("Write UDP: %+v\n", err)
          
        }
      
      case TYPE_SERVER_KEEP_ALIVE:
        targetAddr := make([]byte, len(remoteAddr.IP) + 2)
        copy(targetAddr, remoteAddr.IP)
        binary.LittleEndian.PutUint16(targetAddr[len(remoteAddr.IP):], uint16(remoteAddr.Port))
        cache.Set(string(targetId), targetAddr, time.Now().Add(60 * time.Second))
      
      default:
        log.Printf("ParseMessage: Wrong type id: %d\n", targetId)
      }
    }
  }
  
}
