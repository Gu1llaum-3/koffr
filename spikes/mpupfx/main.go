// Does ListMultipartUploads honour Prefix on MinIO? Throwaway probe.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

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
	out, err := c.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: &b})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  %d televersement(s) inacheve(s) dans %q\n", len(out.Uploads), b)
	_ = aws.String
}
