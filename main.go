package main

import (
	"io"
	"log"
	"net"
	"os"
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

var evLog *os.File

func main() {
	et.Stop()
	var err error
	evLog, err = os.Create("/tmp/evlog")
	if err != nil {
		log.Fatal(err)
	}
	go listenEv()
	listenHttp()
}

var broadcastChan = make(chan struct{})
var et = time.AfterFunc(time.Hour, doEcho)

func doBroadcast() {
	close(broadcastChan)
	broadcastChan = make(chan struct{})
}
func broadcastWithEcho() {
	doBroadcast()
	et.Reset(time.Second * 10)
}
func doEcho() {
	evLog.Write([]byte{byte('e')})
	doBroadcast()
}

func handleEvent(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(time.Second * 5))
	var b [1]byte
	n, err := conn.Read(b[:])
	if err == io.EOF {
		return
	}
	if err != nil || n != 1 {
		log.Println("Failed to handle conn:", err)
		return
	}
	evLog.Write(b[:])
	broadcastWithEcho()
	conn.Close()
}

const (
	statusReady     = 0
	statusKeepAlive = 1
	statusClose     = 2
	statusClosed    = 3
)

func handleHttp(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(time.Second * 20))
	ctx := connCtx{
		conn: conn,
		buf:  make([]byte, BufSize),
	}
	for {
		n, err := conn.Read(ctx.buf[ctx.cnt:])
		if err != nil {
			ctx.mu.Lock()
			defer ctx.mu.Unlock()
			if err == io.EOF {
				if ctx.status == statusKeepAlive || ctx.status == statusClose {
					evLog.Write([]byte{0xff})
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
