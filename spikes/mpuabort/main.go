// What does the service answer when aborting an upload that is not there?
// Throwaway probe.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

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
	b := "backups"
	try := func(label, key, id string) {
		_, err := c.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
			Bucket: &b, Key: aws.String(key), UploadId: aws.String(id),
		})
		if err == nil {
			fmt.Printf("  %-34s succes (rien a signaler)\n", label)
			return
		}
		var api smithy.APIError
		if errors.As(err, &api) {
			fmt.Printf("  %-34s code=%q message=%q\n", label, api.ErrorCode(), api.ErrorMessage())
			return
		}
		fmt.Printf("  %-34s erreur non typee : %v\n", label, err)
	}
	try("id inexistant, cle inexistante", "sources/nope/dump", "bogus-upload-id")
	try("id inexistant, cle existante", "sources/shop/logical/01ABANDONED/dump.pgdump.zst.age", "bogus-upload-id")
}
