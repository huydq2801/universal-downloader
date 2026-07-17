package downloader

import (
	"context"
	"net"
	"time"
)

// DPIBypassConn wraps a net.Conn to bypass Deep Packet Inspection (DPI)
// by fragmenting the first TLS Client Hello packet.
type DPIBypassConn struct {
	net.Conn
	firstWriteDone bool
}

// Write intercepts the first write on the socket (typically the TLS ClientHello record, starting with 0x16).
// It writes the first 5 bytes (TLS Record Header) and sleeps for 20ms before writing the rest.
// This splits the payload (containing the SNI host) into separate TCP packets.
func (c *DPIBypassConn) Write(b []byte) (n int, err error) {
	if !c.firstWriteDone && len(b) > 5 && b[0] == 0x16 {
		c.firstWriteDone = true
		// Write the first 5 bytes of the TLS record header (type, version, length)
		n1, err := c.Conn.Write(b[:5])
		if err != nil {
			return n1, err
		}
		// A small delay to force the TCP stack to push this out as a separate packet
		time.Sleep(20 * time.Millisecond)
		// Write the rest of the ClientHello (containing the SNI extensions)
		n2, err := c.Conn.Write(b[5:])
		return n1 + n2, err
	}
	return c.Conn.Write(b)
}

// NewDPIBypassDialer creates a net.Dialer configured to use public resolvers (Cloudflare/Google)
// to bypass local ISP DNS level filtering.
func NewDPIBypassDialer() *net.Dialer {
	return &net.Dialer{
		Resolver: &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{Timeout: 4 * time.Second}
				conn, err := d.DialContext(ctx, "udp", "1.1.1.1:53")
				if err == nil {
					return conn, nil
				}
				return d.DialContext(ctx, "udp", "8.8.8.8:53")
			},
		},
	}
}

// DPIBypassDialContext wraps dialer connection with DPIBypassConn wrapper
func DPIBypassDialContext(ctx context.Context, dialer *net.Dialer, network, addr string) (net.Conn, error) {
	conn, err := dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	return &DPIBypassConn{Conn: conn}, nil
}
