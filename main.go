package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

const BufSize = 4096

func doListen(path string, handler func(conn net.Conn)) {
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		log.Fatal(err)
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println("Accept failed:", err)
		}
		go handler(conn)
	}

}

func listenHttp() {
	doListen("/tmp/httpsock", handleHttp)
}
func listenEv() {
	doListen("/tmp/evsock", handleEvent)
}
func main() {
	go listenEv()
	listenHttp()
}

var broadcastChan = make(chan struct{})

func doBroadcast() {
	close(broadcastChan)
	broadcastChan = make(chan struct{})
}
func handleEvent(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(time.Second * 5))
	var b [1]byte
	n, err := conn.Read(b[:])
	if err != nil || n != 1 {
		log.Println("Failed to candle conn:", err)
		return
	}
	doBroadcast()
	conn.Close()
}

const (
	statusReady     = 0
	statusKeepAlive = 1
	statusClose     = 2
	statusClosed    = 3
)

type connCtx struct {
	buf []byte
	cnt int

	conn   net.Conn
	host   string
	mu     sync.Mutex
	status uint32
}

func (ctx *connCtx) sendError(errStr string) {
	log.Println(errStr)
	ctx.conn.Write([]byte(fmt.Sprintf("HTTP/1.1 %s\r\nConnection: close\r\n\r\n", errStr)))
	ctx.conn.Close()
	ctx.status = statusClosed
}
func (ctx *connCtx) sendBadRequest() {
	ctx.sendError("400 Bad Request")
}
func (ctx *connCtx) ErrPipeline() {
	log.Println("Client tries to pipeline requests! Forcing Connection: close")
	ctx.status = statusClose
}

var (
	httpSig  = []byte("HTTP/1.1")
	httpGet  = []byte("GET ")
	bNewLine = []byte{10}
	bColon   = []byte(":")
)

func (ctx *connCtx) parseHeaders() {
	if ctx.cnt >= BufSize {
		log.Println("Buffer overflow!")
		ctx.sendError("500 Internal Server Error")
		return
	}
	buf := ctx.buf[:ctx.cnt]
	pos := bytes.IndexByte(buf, 10)
	if pos == -1 {
		return
	}
	line := bytes.TrimSpace(buf[:pos])
	if pos < 14 || !bytes.Equal(line[len(line)-len(httpSig):], httpSig) {
		ctx.sendBadRequest()
		return
	}
	if !bytes.Equal(buf[:len(httpGet)], httpGet) {
		ctx.sendError("405 Method Not Allowed")
	}
	lines := bytes.Split(buf[pos+1:], bNewLine)
	if len(lines) == 0 {
		return
	}
	if len(bytes.TrimSpace(lines[len(lines)-1])) == 0 {
		lines = lines[:len(lines)-1]
	}
	var end, keepalive, err bool
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if end && len(line) > 0 {
			ctx.ErrPipeline()
			break
		}
		if len(line) == 0 {
			end = true
			continue
		}
		kv := bytes.SplitN(line, bColon, 2)
		if len(kv) != 2 {
			err = true
			continue
		}
		switch string(bytes.ToLower(bytes.TrimSpace(kv[0]))) {
		case "host":
			ctx.host = string(bytes.TrimSpace(kv[1]))
		case "connection":
			if string(bytes.ToLower(bytes.TrimSpace(kv[1]))) == "keep-alive" {
				keepalive = true
			}
		}
	}
	if !end {
		return
	}
	if err {
		ctx.sendBadRequest()
		return
	}
	if keepalive {
		ctx.status = statusKeepAlive
	} else {
		ctx.status = statusClose
	}
	ctx.conn.SetReadDeadline(time.Time{})
	go ctx.waitForNotify()
}
func (ctx *connCtx) readAction() bool {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	switch ctx.status {
	case statusReady:
		ctx.parseHeaders()
	case statusKeepAlive:
		ctx.ErrPipeline()
	}
	if ctx.status != statusReady {
		ctx.cnt = 0
	}
	return ctx.status == statusClosed
}

func handleHttp(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(time.Second * 20))
	ctx := connCtx{
		conn: conn,
		host: "localhost",
		buf:  make([]byte, BufSize),
	}
	for {
		n, err := conn.Read(ctx.buf[ctx.cnt:])
		if err != nil {
			ctx.mu.Lock()
			defer ctx.mu.Unlock()
			if err == io.EOF {
				if ctx.status == statusKeepAlive || ctx.status == statusClose {
					doBroadcast()
				}
			} else if ctx.status != statusClosed {
				log.Println("Connection error:", err)
			}
			conn.Close()
			ctx.status = statusClosed
			return
		}
		ctx.cnt += n
		if ctx.readAction() {
			return
		}
	}
}

func (ctx *connCtx) waitForNotify() {
	select {
	case <-broadcastChan:
	case <-time.After(time.Second * 60):
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if ctx.status == statusClosed {
		return
	}
	var conn string
	if ctx.status == statusClose {
		conn = "close"
	} else {
		conn = "keep-alive"
	}
	bs := []byte(fmt.Sprintf("HTTP/1.1 204 No Content\r\nHost: %s\r\nConnection: %s\r\n\r\n", ctx.host, conn))
	n, err := ctx.conn.Write(bs)
	if err != nil || n < len(bs) {
		log.Println("Write error", err)
		ctx.status = statusClose
	}
	if ctx.status == statusClose {
		ctx.conn.Close()
		ctx.status = statusClosed
	} else {
		ctx.status = statusReady
	}
}
