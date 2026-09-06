// How long a network outage does the upload manager actually survive?
//
// Puts a TCP proxy in front of MinIO and severs every connection through it for
// a chosen duration, mid-upload, then lets traffic flow again. Reports whether
// the upload recovered. Run once per outage length, with the retryer Koffr
// configures today (the SDK default) and with a deliberately longer one.
// Throwaway probe.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsretry "github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

var (
	severed atomic.Bool
	mu      sync.Mutex
	live    = map[net.Conn]struct{}{}
)

// sever refuses new connections and tears down the ones already open. Closing
// only the new ones proves nothing: the SDK pools connections, so an upload in
// flight keeps using a socket opened before the outage began.
func sever(on bool) {
	severed.Store(on)
	if !on {
		return
	}
	mu.Lock()
	for c := range live {
		c.Close()
	}
	live = map[net.Conn]struct{}{}
	mu.Unlock()
}

// proxy forwards to MinIO, except while severed.
func proxy(ln net.Listener, target string) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			if severed.Load() {
				return
			}
			u, err := net.Dial("tcp", target)
			if err != nil {
				return
			}
			defer u.Close()
			mu.Lock()
			live[c] = struct{}{}
			live[u] = struct{}{}
			mu.Unlock()
			defer func() {
				mu.Lock()
				delete(live, c)
				delete(live, u)
				mu.Unlock()
			}()
			done := make(chan struct{}, 2)
			cp := func(dst, src net.Conn) { io.Copy(dst, src); done <- struct{}{} } //nolint:errcheck
			go cp(u, c)
			go cp(c, u)
			<-done
		}(c)
	}
}

// slowReader feeds the uploader at a fixed rate so the outage lands mid-upload
// rather than after it. A real dump arrives at pg_dump's pace, not instantly.
type slowReader struct {
	left int
	rate int
	last time.Time
}

func (s *slowReader) Read(p []byte) (int, error) {
	if s.left <= 0 {
		return 0, io.EOF
	}
	if s.last.IsZero() {
		s.last = time.Now()
	}
	n := min(len(p), 256<<10, s.left)
	want := time.Duration(n) * time.Second / time.Duration(s.rate)
	if d := time.Until(s.last.Add(want)); d > 0 {
		time.Sleep(d)
	}
	s.last = time.Now()
	s.left -= n
	for i := range p[:n] {
		p[i] = byte(i)
	}
	return n, nil
}

func run(name string, outage time.Duration, attempts int, maxBackoff time.Duration) {
	ctx := context.Background()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()
	go proxy(ln, "127.0.0.1:9030")

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			os.Getenv("KOFFR_S3_KEY"), os.Getenv("KOFFR_S3_SECRET"), "")),
		config.WithRetryer(func() aws.Retryer {
			return awsretry.NewStandard(func(o *awsretry.StandardOptions) {
				if attempts > 0 {
					o.MaxAttempts = attempts
					o.MaxBackoff = maxBackoff
					o.Backoff = awsretry.BackoffDelayerFunc(
						func(int, error) (time.Duration, error) { return maxBackoff, nil })
				}
			})
		}))
	if err != nil {
		log.Fatal(err)
	}
	c := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String("http://" + ln.Addr().String())
		o.UsePathStyle = true
	})
	up := manager.NewUploader(c, func(u *manager.Uploader) { u.PartSize = 8 << 20 }) //nolint:staticcheck

	bucket := "backups"
	key := fmt.Sprintf("probe/%s-%d", name, time.Now().UnixNano())
	// 120 MiB at 20 MiB/s: six seconds of upload, ample room for an outage.
	body := &slowReader{left: 120 << 20, rate: 20 << 20}

	sever(false)
	go func() {
		time.Sleep(2 * time.Second)
		sever(true)
		time.Sleep(outage)
		sever(false)
	}()

	start := time.Now()
	_, err = up.Upload(ctx, &s3.PutObjectInput{Bucket: &bucket, Key: &key, Body: body})
	took := time.Since(start).Round(100 * time.Millisecond)
	if err != nil {
		msg := err.Error()
		if len(msg) > 90 {
			msg = msg[:90] + "..."
		}
		fmt.Printf("  %-34s coupure %-5s -> ECHEC apres %-7s %s\n", name, outage, took, msg)
	} else {
		fmt.Printf("  %-34s coupure %-5s -> RECUPERE en %s\n", name, outage, took)
	}

	// Did the failed upload leave parts behind?
	ups, err := c.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: &bucket})
	if err == nil {
		n := 0
		for _, u := range ups.Uploads {
			if *u.Key == key {
				n++
			}
		}
		if n > 0 {
			fmt.Printf("  %-34s   ... et %d televersement(s) inacheve(s) laisse(s) par CETTE tentative\n", "", n)
		}
	}
	_ = bytes.MinRead
}

func main() {
	const interval = 10 * time.Second
	// A 30s window means 5 attempts, so retrying alone can last at most 40s.
	// If giving up under a long outage takes about twice that, the cleanup
	// abort is going through the retryer too.
	fmt.Println("=== renoncement : fenetre 30s (5 tentatives, 40s de reessais), coupure 4m ===")
	run("fenetre 30s", 4*time.Minute, int(30*time.Second/interval)+2, interval)
}
