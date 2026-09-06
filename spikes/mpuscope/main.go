// Is ListMultipartUploads scoped to the bucket it is asked about?
//
// Two fresh buckets, one upload in each, then each is listed. If a listing of A
// mentions B's upload, then a sweep that filters on key prefix alone can abort
// another repository's running backup. Throwaway probe.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
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

	stamp := time.Now().UnixNano()
	buckets := []string{fmt.Sprintf("scope-a-%d", stamp), fmt.Sprintf("scope-b-%d", stamp)}
	for _, b := range buckets {
		if _, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(b)}); err != nil {
			log.Fatal(err)
		}
		key := fmt.Sprintf("sources/shop/logical/01IN-%s/dump.zst.age", b)
		cr, err := c.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
			Bucket: aws.String(b), Key: aws.String(key)})
		if err != nil {
			log.Fatal(err)
		}
		if _, err := c.UploadPart(ctx, &s3.UploadPartInput{
			Bucket: aws.String(b), Key: aws.String(key), UploadId: cr.UploadId,
			PartNumber: aws.Int32(1), Body: bytes.NewReader(bytes.Repeat([]byte("x"), 5<<20)),
		}); err != nil {
			log.Fatal(err)
		}
	}

	// Is ListParts scoped where ListMultipartUploads is not? If it is, asking
	// it about a candidate is a reliable ownership check.
	for _, b := range buckets {
		other := buckets[0]
		if b == other {
			other = buckets[1]
		}
		out0, err := c.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: aws.String(other)})
		if err != nil {
			log.Fatal(err)
		}
		for _, u := range out0.Uploads {
			if !contains(*u.Key, other) {
				continue
			}
			_, err := c.ListParts(ctx, &s3.ListPartsInput{
				Bucket: aws.String(b), Key: u.Key, UploadId: u.UploadId})
			fmt.Printf("  ListParts(%s, une cle de %s) -> %v\n", short(b), short(other), errText(err))
		}
	}

	for _, b := range buckets {
		out, err := c.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: aws.String(b)})
		if err != nil {
			log.Fatal(err)
		}
		own, foreign := 0, 0
		for _, u := range out.Uploads {
			if k := *u.Key; len(k) > 0 && contains(k, b) {
				own++
			} else {
				foreign++
			}
		}
		fmt.Printf("  listage de %s : %d a lui, %d a quelqu'un d'autre\n", b, own, foreign)
	}
}

func short(b string) string { return b[:7] }

func errText(err error) string {
	if err == nil {
		return "SUCCES (non scope)"
	}
	var api smithy.APIError
	if errors.As(err, &api) {
		return "refus " + api.ErrorCode()
	}
	return err.Error()
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
