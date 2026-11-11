// nettest: minimal TCP throughput and round-trip-latency probe (no dependencies).
//
//	nettest -serve :20100                     # echo/sink server
//	nettest -to host:20100 -secs 10           # throughput (MB/s) then 20k 64-byte ping-pongs (p50/p99 in µs)
package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"time"
)

func main() {
	serve := flag.String("serve", "", "listen address")
	to := flag.String("to", "", "server address")
	secs := flag.Int("secs", 10, "throughput seconds")
	pings := flag.Int("pings", 20000, "ping-pong count")
	flag.Parse()
	if *serve != "" {
		ln, err := net.Listen("tcp", *serve)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		for {
			c, err := ln.Accept()
			if err != nil {
				continue
			}
			go handle(c)
		}
	}
	// throughput: mode byte 'T', then stream
	c, err := net.Dial("tcp", *to)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	c.Write([]byte{'T'})
	buf := make([]byte, 1<<20)
	var n int64
	end := time.Now().Add(time.Duration(*secs) * time.Second)
	start := time.Now()
	for time.Now().Before(end) {
		k, err := c.Write(buf)
		if err != nil {
			break
		}
		n += int64(k)
	}
	el := time.Since(start).Seconds()
	c.Close()
	// latency: mode byte 'P', then 64-byte echoes
	p, err := net.Dial("tcp", *to)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	p.(*net.TCPConn).SetNoDelay(true)
	p.Write([]byte{'P'})
	msg := make([]byte, 64)
	lat := make([]float64, 0, *pings)
	for i := 0; i < *pings; i++ {
		t := time.Now()
		p.Write(msg)
		if _, err := io.ReadFull(p, msg); err != nil {
			break
		}
		lat = append(lat, float64(time.Since(t).Microseconds()))
	}
	p.Close()
	sort.Float64s(lat)
	q := func(f float64) float64 { return lat[int(f*float64(len(lat)-1))] }
	fmt.Printf("throughput=%.1f MB/s (%.0f Mbit/s) rtt_us p50=%.0f p99=%.0f n=%d\n", float64(n)/el/1e6, float64(n)*8/el/1e6, q(0.5), q(0.99), len(lat))
}

func handle(c net.Conn) {
	defer c.Close()
	c.(*net.TCPConn).SetNoDelay(true)
	mode := make([]byte, 1)
	if _, err := io.ReadFull(c, mode); err != nil {
		return
	}
	if mode[0] == 'T' {
		io.Copy(io.Discard, c)
		return
	}
	msg := make([]byte, 64)
	for {
		if _, err := io.ReadFull(c, msg); err != nil {
			return
		}
		c.Write(msg)
	}
}
