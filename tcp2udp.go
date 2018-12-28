package main

import (
	"bufio"
	"encoding/binary"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

type ConnPair struct {
	tcpConn     *net.TCPConn
	udpConn     *net.UDPConn
	tcpReadBuf  *bufio.Reader
	tcpWriteBuf *bufio.Writer
	wg sync.WaitGroup
	lastTCPReadTime int64
	lastUDPReadTime int64
	t2u *TCP2UDP
}

func newConnPair(tcpConn *net.TCPConn, udpConn *net.UDPConn, t2u *TCP2UDP) *ConnPair {
	return &ConnPair{
		tcpConn:     tcpConn,
		udpConn:     udpConn,
		tcpReadBuf:  bufio.NewReader(tcpConn),
		tcpWriteBuf: bufio.NewWriter(tcpConn),
		lastTCPReadTime: time.Now().Unix(),
		lastUDPReadTime: time.Now().Unix(),
		t2u: t2u,
	}
}

func (p *ConnPair) udpLoop(maxUDPLength uint32) {
	packet := make([]byte, maxUDPLength)
	bodyLenBuf := make([]byte, 2)
	for {
		udpRecvdBytes, err := p.udpConn.Read(packet)
		if err != nil {
			log.Printf("[ERR] %p UDP recv failed. %v %v", p, udpRecvdBytes, err)
			break
		}
		p.lastUDPReadTime = time.Now().Unix()
		log.Printf("[INF] %p UDP recvd %v", p, udpRecvdBytes)

		fr := p.t2u.UDPReadFilter(p, packet[0:udpRecvdBytes])
		if !fr {
			log.Printf("[INF] %p UDP not trans to tcp by filter.", p)
			continue
		}

		binary.BigEndian.PutUint16(bodyLenBuf, uint16(udpRecvdBytes))
		_, err = p.tcpWriteBuf.Write(bodyLenBuf)
		if err != nil {
			log.Printf("[ERR] %p TCP sent header failed. %v", p, err)
			break
		}
		_, err = p.tcpWriteBuf.Write(packet[0:udpRecvdBytes])
		if err != nil {
			log.Printf("[ERR] %p TCP sent body failed. %v", p, err)
			break
		}
		err = p.tcpWriteBuf.Flush()
		if err != nil {
			log.Printf("[ERR] %p TCP send flush failed. %v", p, err)
			break
		}
		log.Printf("[INF] %p TCP sent. body len:%v", p, udpRecvdBytes)
	}
	p.tcpConn.Close()
	p.tcpConn.Close()
	p.wg.Done()
}

func (p *ConnPair) tcpLoop(maxUDPLength uint32) {
	bodyLenBuf := make([]byte, 2)
	for {
		_, err := io.ReadFull(p.tcpReadBuf, bodyLenBuf)
		if err != nil {
			log.Printf("[ERR] %p TCP recv head failed. err: %v", p, err)
			break
		}
		p.lastTCPReadTime = time.Now().Unix()
		bodyLen := binary.BigEndian.Uint16(bodyLenBuf)
		log.Printf("[INF] %p TCP recvd bodyLen in head field. bodyLen: %v", p, bodyLen)
		if bodyLen > uint16(maxUDPLength) {
			log.Printf("[ERR] %p TCP Body len too big. bodyLen: %v", p, bodyLen)
			break
			// TODO test
			//bodyLen = 4
		}

		packet := make([]byte, bodyLen)
		_, err = io.ReadFull(p.tcpReadBuf, packet)
		if err != nil {
			log.Printf("[ERR] %p TCP recv body failed. err: %v", p, err)
			break
		}
		p.lastTCPReadTime = time.Now().Unix()
		log.Printf("[INF] %p TCP recvd body. len: %v", p, bodyLen)

		fr := p.t2u.tcpReadFilter(p, packet)
		if !fr {
			log.Printf("[INF] %p TCP not trans to udp by filter.", p)
			continue
		}

		_, err = p.udpConn.Write(packet)
		if err != nil {
			log.Printf("[ERR] %p UDP sent failed. err: %v", p, err)
			break
		}
		log.Printf("[INF] %p UDP sent packet. bodyLen: %v", p, bodyLen)
	}
	p.tcpConn.Close()
	p.udpConn.Close()
	p.wg.Done()
}

type TCP2UDP struct {
	maxUDPLength uint32
	tcpAddr      string
	udpAddr      string
	pairs        map[*ConnPair]bool
	mutex        *sync.Mutex
	tcpReadTimeoutSec int64
	udpReadTimeoutSec int64
	tcpReadFilter TCPReadFilter
	UDPReadFilter UDPReadFilter
}

func NewTCP2UDPWithDefaultConfig() *TCP2UDP {
	return NewTCP2UDPWithConfig(2048)
}

func NewTCP2UDPWithConfig(maxUDPLength uint32) *TCP2UDP {
	return &TCP2UDP{
		maxUDPLength: maxUDPLength,
		pairs:        make(map[*ConnPair]bool),
		mutex: &sync.Mutex{},
	}
}

type TCPReadFilter func(pair *ConnPair, packet []byte) bool
type UDPReadFilter func(pair *ConnPair, packet []byte) bool

func (t2u *TCP2UDP) SetFilter(trf TCPReadFilter, urf UDPReadFilter) {
	t2u.tcpReadFilter = trf
	t2u.UDPReadFilter = urf
}

func (t2u *TCP2UDP) SetTimeout(tcpReadTimeoutSec int64, udpReadTimeoutSec int64) {
	t2u.tcpReadTimeoutSec = tcpReadTimeoutSec
	t2u.udpReadTimeoutSec = udpReadTimeoutSec
}

func (t2u *TCP2UDP) watchDogLoop() {
	ticker := time.NewTicker(time.Second * 1)
	for _ = range ticker.C {
		nowTime := time.Now().Unix()
		t2u.mutex.Lock()
		log.Printf("[INF] Pairs len: %v", len(t2u.pairs))

		for pair, _ := range t2u.pairs {
			if (t2u.tcpReadTimeoutSec != 0) && ((nowTime - pair.lastTCPReadTime) > t2u.tcpReadTimeoutSec) {
				log.Printf("[INF] %p TCP read timeout. last: %v", pair, pair.lastTCPReadTime)
				pair.tcpConn.Close()
				pair.udpConn.Close()
			}
			if (t2u.udpReadTimeoutSec != 0) && ((nowTime - pair.lastUDPReadTime) > t2u.udpReadTimeoutSec) {
				log.Printf("[INF] %p UDP read timeout. last: %v", pair, pair.lastUDPReadTime)
				pair.tcpConn.Close()
				pair.udpConn.Close()
			}
		}
		t2u.mutex.Unlock()
	}
}

func (t2u *TCP2UDP) handleNewTCPConn(tcpConn *net.TCPConn) {
	log.Printf("[INF] Handle new TCP Conn. remote: %v", tcpConn.RemoteAddr().String())

	udpAddr, err := net.ResolveUDPAddr("udp4", t2u.udpAddr)
	if err != nil {
		log.Printf("[ERR] ResolveUDPAddr failed. err: %v", err)
		return
	}
	udpConn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		log.Printf("[ERR] DailUDP failed. err: %v", err)
		return
	}

	connPair := newConnPair(tcpConn, udpConn, t2u)
	log.Printf("[INF] %p Create pair. tcp remote: %v, udp remote: %v", connPair, tcpConn.RemoteAddr().String(), udpConn.RemoteAddr().String())
	t2u.mutex.Lock()
	t2u.pairs[connPair] = true
	t2u.mutex.Unlock()
	connPair.wg.Add(2)

	go connPair.tcpLoop(t2u.maxUDPLength)
	go connPair.udpLoop(t2u.maxUDPLength)

	connPair.wg.Wait()
	log.Printf("[INF] %p Destory pair. tcp remote: %v, udp remote: %v", connPair, tcpConn.RemoteAddr().String(), udpConn.RemoteAddr().String())
	t2u.mutex.Lock()
	delete(t2u.pairs, connPair)
	t2u.mutex.Unlock()
}

func (t2u *TCP2UDP) Run(tcpAddr string, udpAddr string) {
	go t2u.watchDogLoop()
	t2u.tcpAddr = tcpAddr
	t2u.udpAddr = udpAddr
	ta, err := net.ResolveTCPAddr("tcp4", tcpAddr)
	if err != nil {
		log.Printf("[ERR] ResolveTCPAddr failed. err: %v", err)
		return
	}
	l, err := net.ListenTCP("tcp", ta)
	if err != nil {
		log.Printf("[ERR] ListenTCP failed. err: %v", err)
		return
	}
	defer l.Close()

	for {
		conn, err := l.AcceptTCP()
		if err != nil {
			log.Printf("[ERR] AcceptTCP failed. err: %v", err)
			continue
		}

		go t2u.handleNewTCPConn(conn)
	}
}
