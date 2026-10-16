// Package chaos is a deliberately bad network: an HTTP(S) proxy or TCP forwarder that cuts
// (or freezes) every connection after a number of bytes. sabr's tests use it, and
// `sabr chaos` exposes it so anyone can reproduce a flaky connection and compare tools.
package chaos

import (
	"bufio"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Proxy cuts each connection after about Cut bytes from server to client.
type Proxy struct {
	Cut    int64         // bytes per connection before the cut (0 = never)
	Jitter float64       // 0.5 = cut somewhere between 50% and 150% of Cut
	Freeze bool          // stop forwarding instead of closing (tests stall detection)
	To     string        // TCP mode: forward every connection to this host:port
	Delay  time.Duration // extra latency per read
	Log    func(format string, a ...any)

	ln       net.Listener
	Conns    atomic.Int64
	Cuts     atomic.Int64
	Bytes    atomic.Int64
	wg       sync.WaitGroup
	closing  atomic.Bool
	mu       sync.Mutex
	children map[net.Conn]bool
}

// Listen starts the proxy on addr (e.g. 127.0.0.1:0) and returns the bound address.
func (p *Proxy) Listen(addr string) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	p.ln = ln
	p.children = map[net.Conn]bool{}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.track(c, true)
			p.wg.Add(1)
			go func() { defer p.wg.Done(); p.handle(c) }()
		}
	}()
	return ln.Addr().String(), nil
}

// Close stops the proxy and drops every open connection.
func (p *Proxy) Close() error {
	p.closing.Store(true)
	err := p.ln.Close()
	p.mu.Lock()
	for c := range p.children {
		c.Close()
	}
	p.mu.Unlock()
	return err
}

func (p *Proxy) track(c net.Conn, add bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if add {
		p.children[c] = true
	} else {
		delete(p.children, c)
	}
}

func (p *Proxy) logf(f string, a ...any) {
	if p.Log != nil {
		p.Log(f, a...)
	}
}

func (p *Proxy) budget() int64 {
	if p.Cut <= 0 {
		return -1
	}
	if p.Jitter <= 0 {
		return p.Cut
	}
	f := 1 - p.Jitter + rand.Float64()*2*p.Jitter
	return max(1024, int64(float64(p.Cut)*f))
}

func (p *Proxy) handle(c net.Conn) {
	defer func() { c.Close(); p.track(c, false) }()
	p.Conns.Add(1)
	if p.To != "" {
		up, err := net.DialTimeout("tcp", p.To, 15*time.Second)
		if err != nil {
			p.logf("dial %s: %v", p.To, err)
			return
		}
		p.pipe(c, up, nil, p.To)
		return
	}
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if req.Method == http.MethodConnect {
		up, err := net.DialTimeout("tcp", req.Host, 15*time.Second)
		if err != nil {
			io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
			return
		}
		io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
		p.pipe(c, up, br, req.Host)
		return
	}
	// plain http forward proxying (one request per connection)
	host := req.URL.Host
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	up, err := net.DialTimeout("tcp", host, 15*time.Second)
	if err != nil {
		io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return
	}
	req.RequestURI = ""
	req.Header.Set("Connection", "close")
	req.Header.Del("Proxy-Connection")
	if err := req.Write(up); err != nil {
		up.Close()
		return
	}
	p.pipe(c, up, br, host)
}

// pipe copies both ways; the server→client direction is metered and cut.
func (p *Proxy) pipe(client, up net.Conn, pending *bufio.Reader, label string) {
	p.track(up, true)
	defer func() { up.Close(); p.track(up, false) }()
	done := make(chan struct{}, 2)
	go func() {
		var src io.Reader = client
		if pending != nil {
			src = pending
		}
		io.Copy(up, src)
		done <- struct{}{}
	}()
	go func() {
		limit := p.budget()
		var n int64
		buf := make([]byte, 16*1024)
		for {
			if p.Delay > 0 {
				time.Sleep(p.Delay)
			}
			k, err := up.Read(buf)
			if k > 0 {
				if limit >= 0 && n+int64(k) > limit {
					k = int(limit - n)
					client.Write(buf[:k])
					p.Bytes.Add(int64(k))
					p.Cuts.Add(1)
					if p.Freeze {
						p.logf("freeze %s after %d bytes", label, limit)
						for !p.closing.Load() {
							time.Sleep(200 * time.Millisecond)
						}
					} else {
						p.logf("cut %s after %d bytes", label, limit)
					}
					break
				}
				if _, werr := client.Write(buf[:k]); werr != nil {
					break
				}
				n += int64(k)
				p.Bytes.Add(int64(k))
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	<-done
	client.Close()
	up.Close()
	<-done
}
