// Shared helper for cutting a transfer mid-flight, plus its own test.
//
// It lives outside the e2e build tag so the mechanism itself can be verified
// locally: a harness that quietly failed to cut would make the real resume test
// fail against MYBOX for reasons that have nothing to do with MYBOX.
package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/overworks/mybox-cli/internal/transfer"
)

// errCut marks the deliberate mid-transfer connection failure.
var errCut = errors.New("connection cut on purpose")

// cuttingDialer hands out connections that die once a set number of bytes have
// been written, simulating a transfer dropping mid-flight.
//
// It counts bytes at the TCP layer, below TLS, so the cut point is approximate —
// which is fine, since all that matters is that it lands inside the body.
type cuttingDialer struct {
	after   int64
	written atomic.Int64
}

func (d *cuttingDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	c, err := (&net.Dialer{Timeout: 30 * time.Second}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	return &cuttingConn{Conn: c, d: d}, nil
}

type cuttingConn struct {
	net.Conn
	d *cuttingDialer
}

func (c *cuttingConn) Write(b []byte) (int, error) {
	remaining := c.d.after - c.d.written.Load()
	if remaining <= 0 {
		c.cut()
		return 0, errCut
	}
	if int64(len(b)) <= remaining {
		n, err := c.Conn.Write(b)
		c.d.written.Add(int64(n))
		return n, err
	}

	// Deliver the last partial chunk, then drop the connection with the
	// declared Content-Length unfulfilled.
	n, err := c.Conn.Write(b[:remaining])
	c.d.written.Add(int64(n))
	if err != nil {
		return n, err
	}
	c.cut()
	return n, errCut
}

// cut closes the connection after giving the kernel a moment to put what was
// already written on the wire. Returning an error without this would have Go
// tear the socket down while the tail of the body is still buffered, and the
// server would retain less than intended.
func (c *cuttingConn) cut() {
	time.Sleep(500 * time.Millisecond)
	_ = c.Conn.Close()
}

func TestCuttingDialerDropsTheConnectionMidBody(t *testing.T) {
	const total = 1 << 20
	const cutAfter = 64 << 10

	var received atomic.Int64
	// The handler runs on the server's own goroutine and is still draining the
	// body when Upload returns on the client side. Without this signal the
	// assertions race the read, which shows up as an occasional "the server
	// received nothing" on a loaded machine.
	drained := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body) // the error is the point; count what arrived
		received.Add(n)
		close(drained)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	dialer := &cuttingDialer{after: cutAfter}
	c := &transfer.Client{
		HTTPClient: &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext}},
		UserAgent:  "mybox-cli/test",
	}

	_, err := c.Upload(context.Background(), transfer.UploadRequest{
		URL: srv.URL, Body: bytes.NewReader(make([]byte, total)),
		FileName: "big.bin", Size: total, Strategy: transfer.DefaultStrategy,
	})

	// The upload must fail: the request declared its full length and then the
	// socket died, which is exactly the interruption the resume test needs.
	if err == nil {
		t.Fatal("the upload completed; the connection was never cut")
	}
	if wrote := dialer.written.Load(); wrote > cutAfter {
		t.Errorf("cut after %d bytes, past the %d limit", wrote, cutAfter)
	}

	select {
	case <-drained:
	case <-time.After(30 * time.Second):
		t.Fatal("the server never finished reading the body")
	}

	got := received.Load()
	if got == 0 {
		t.Error("the server received nothing; the cut lands too early")
	}
	if got >= total {
		t.Errorf("the server received all %d bytes; nothing was cut", got)
	}
	t.Logf("declared %d bytes, cut after the server had received %d", total, got)
}

func TestCuttingDialerLeavesUncutTransfersAlone(t *testing.T) {
	const total = 1 << 10

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"resourceId":"R","name":"small.bin","fileSize":1024}`)
	}))
	t.Cleanup(srv.Close)

	// A generous limit must not interfere, or the harness would be cutting
	// transfers it was never asked to touch.
	dialer := &cuttingDialer{after: 10 << 20}
	c := &transfer.Client{
		HTTPClient: &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext}},
		UserAgent:  "mybox-cli/test",
	}

	res, err := c.Upload(context.Background(), transfer.UploadRequest{
		URL: srv.URL, Body: bytes.NewReader(make([]byte, total)),
		FileName: "small.bin", Size: total, Strategy: transfer.DefaultStrategy,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if res.ResourceID != "R" {
		t.Errorf("ResourceID = %q", res.ResourceID)
	}
}
