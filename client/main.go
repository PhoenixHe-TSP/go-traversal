package main

import (
  "github.com/urfave/cli"
  "time"
  "math/rand"
  "net"
  "log"
  "os"
  "errors"
  "encoding/binary"
  "sync/atomic"
  "unsafe"
  "bytes"
)

var (
  // atomic
  serverAddr, localAddr *net.UDPAddr
  globalSessionId       uint32
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
  TYPE_DATA              = 4
)

func parseMessage(packet []byte) (data []byte, sessionId uint32, typeId uint8, err error) {
  if len(packet) < 5 {
    return nil, nil, nil, errors.New("Message too short")
  }
  
  sessionId = binary.LittleEndian.Uint32(packet[:4])
  packet = packet[4:]
  typeId = packet[0]
  data = packet[1:]
  
  return
}

func makeMessage(packet []byte, sessionId uint32, typeId uint8, content []byte) (size int) {
  binary.LittleEndian.PutUint32(packet[:4], sessionId)
  packet[0] = typeId
  packet = packet[1:]
  copy(packet, content)
  return len(content) + 5
}

func udpAddrEqual(a, b *net.UDPAddr) bool {
  if a == nil || b == nil {
    return false
  }
  if !bytes.Equal(a.IP, b.IP) {
    return false
  }
  return a.Port == b.Port;
}

func clientForwardRecv(forwardListener *net.UDPConn, listenAddr *net.UDPAddr) {
  buf := make([]byte, 1500)
  headerSize := makeMessage(buf, globalSessionId, TYPE_DATA, make([]byte, 0))
  recvBuf := buf[headerSize:]
  
  var serverAddrCached *net.UDPAddr
  var serverSendConn *net.UDPConn
  
  for {
    size, remoteAddr, err := forwardListener.ReadFromUDP(recvBuf)
    if err != nil {
      log.Printf("ClientForwardRecv: %v+\n", err)
      continue
    }
    
    atomic.StorePointer(&unsafe.Pointer(localAddr), unsafe.Pointer(remoteAddr))
    
    sa := (*net.UDPAddr)(atomic.LoadPointer(&unsafe.Pointer(serverAddr)))
    if !udpAddrEqual(sa, serverAddrCached) {
      serverAddrCached = sa
      if serverAddrCached != nil {
        serverSendConn, _ = net.DialUDP("udp", listenAddr, serverAddrCached)
      }
    }
    if serverSendConn == nil {
      log.Print("DATA: Send to server before ready\n")
      continue
    }
    
    _, err = serverSendConn.Write(buf[:headerSize + size])
    if err != nil {
      log.Printf("DATA: Send to server: %v+\n", err)
    }
  }
  
}

func clientMainRecv(relayConn *net.UDPConn, targetId uint32, forwardAddr *net.UDPAddr) {
  buf := make([]byte, 1500)
  var localAddrCached *net.UDPAddr
  var localSendConn *net.UDPConn
  
  for {
    size, remoteAddr, err := relayConn.ReadFromUDP(buf)
    if err != nil {
      log.Printf("ClientMainRecv: %v+\n", err)
      continue
    }
    
    data, sessionId, typeId, err := parseMessage(buf[:size])
    if err != nil {
      if err != nil {
        log.Printf("ParseMessage: %+v\n", err)
        continue
      }
    }
    
    switch typeId {
    case TYPE_QUERY_ANSWER:
      if sessionId != 0 {
        log.Printf("ParseMessage: Wrong session id: %d\n", sessionId)
        continue
      }
      if len(data) < 10 {
        log.Print("QUERY_ANWSER: Packet too short\n")
        continue
      }
      tid := binary.LittleEndian.Uint32(data[:4])
      data = data[4:]
      if tid != targetId {
        log.Printf("QUERY_ANSWER: Wrong target id: %d\n", tid)
        continue
      }
      ip := make([]byte, len(data) - 2)
      copy(ip, data)
      if err != nil {
        log.Print("QUERY_ANSWER: Cannot parse IP\n")
        continue
      }
      port := binary.LittleEndian.Uint16(data[len(data) - 2:])
      
      atomic.StorePointer(&unsafe.Pointer(serverAddr), unsafe.Pointer(
        &net.UDPAddr{ip, int(port), nil}))
    
    case TYPE_DATA:
      if sessionId != globalSessionId {
        log.Print("DATA: Wrong sessionId\n")
        continue
      }
      // TODO: notify relay if this address changed?
      atomic.StorePointer(&unsafe.Pointer(serverAddr), unsafe.Pointer(remoteAddr))
      
      la := (*net.UDPAddr)(atomic.LoadPointer(&unsafe.Pointer(localAddr)))
      if !udpAddrEqual(la, localAddrCached) {
        localAddrCached = la
        if localAddrCached != nil {
          localSendConn, _ = net.DialUDP("udp", forwardAddr, localAddrCached)
        }
      }
      if localSendConn == nil {
        log.Print("DATA: Write to local before ready\n")
        continue
      }
      
      _, err := localSendConn.Write(data)
      if err != nil {
        log.Printf("DATA: Write to local: %v+\n", err)
      }
    
    default:
      log.Printf("ParseMessage: Unknown typeId: %d\n", typeId)
    }
  }
}

func clientQuerySend(relayConn *net.UDPConn, targetId int) {
  query := make([]byte, 1500)
  t := make([]byte, 4)
  binary.LittleEndian.PutUint32(t, uint32(targetId))
  size := makeMessage(query, 0, TYPE_QUERY, t)
  query = query[:size]
  
  for {
    _, err := relayConn.Write(query)
    if err != nil {
      log.Printf("ClientQuerySend: %v+\n", err)
    }
    
    timeout := 10
    target := (*net.UDPAddr)(atomic.LoadPointer(&unsafe.Pointer(serverAddr)))
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
      Name:  "listen,l",
      Value: ":8008",
      Usage: "local listen address",
    },
    cli.StringFlag{
      Name:  "forward,f",
      Value: ":8001",
      Usage: "local forward address",
    },
    cli.StringFlag{
      Name:  "relay,r",
      Value: "127.0.0.1:8007",
      Usage: "relay server address",
    },
    cli.StringFlag{
      Name:  "target-id,i",
      Value: "7",
      Usage: "target server id",
    },
  }
  
  myApp.Action = func(c *cli.Context) error {
    listenAddr, err := net.ResolveUDPAddr("udp4", c.String("listen"))
    checkError(err)
    listener, err := net.ListenUDP("udp4", listenAddr)
    checkError(err)
    
    relayAddr, err := net.ResolveUDPAddr("udp4", c.String("relay"))
    checkError(err)
    relayConn, err := net.DialUDP("udp4", listenAddr, relayAddr)
    checkError(err)
    
    forwardAddr, err := net.ResolveUDPAddr("udp", c.String("forward"))
    checkError(err)
    forwardListener, err := net.ListenUDP("udp", forwardAddr)
    checkError(err)
    
    targetId := c.Int("target-id")
    globalSessionId = rand.Uint32()
    
    go clientQuerySend(relayConn, targetId)
    go clientForwardRecv(forwardListener, listenAddr)
    go clientMainRecv(listener, uint32(targetId), forwardAddr)
    
    return nil
  }
  
}
