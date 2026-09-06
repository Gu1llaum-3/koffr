// How much does it cost to create enough unfinished uploads to force paging,
// and does MinIO page at 1000? Throwaway probe.
package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func main() {
	ctx := context.Background()
	cfg, _ := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			os.Getenv("KOFFR_S3_KEY"), os.Getenv("KOFFR_S3_SECRET"), "")))
	c := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String("http://127.0.0.1:9030")
		o.UsePathStyle = true
	})
	b := "sweeptest"
	c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &b}) //nolint:errcheck

	const n = 3
	start := time.Now()
	sem := make(chan struct{}, 48)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			key := fmt.Sprintf("sources/shop/logical/%05d/dump.zst.age", i)
			if _, err := c.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
				Bucket: &b, Key: aws.String(key),
			}); err != nil {
				fmt.Println("  erreur:", err)
			}
		}(i)
	}
	wg.Wait()
	fmt.Printf("  %d televersements crees en %s\n", n, time.Since(start).Round(time.Millisecond))

	for _, mx := range []int32{0, 1, 10, 1000} {
		in := &s3.ListMultipartUploadsInput{Bucket: &b}
		if mx > 0 {
			in.MaxUploads = aws.Int32(mx)
		}
		out, err := c.ListMultipartUploads(ctx, in)
		if err != nil {
			fmt.Println("  erreur de listage:", err)
			continue
		}
		fmt.Printf("  MaxUploads=%-5d -> %d rendus, tronquee=%v, NextKeyMarker=%q\n",
			mx, len(out.Uploads), aws.ToBool(out.IsTruncated), aws.ToString(out.NextKeyMarker))
	}
}
