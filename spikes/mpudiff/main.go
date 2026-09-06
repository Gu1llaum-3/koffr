// Does MinIO forget an aborted upload that never received a part?
// Creates one upload with a part and one without, then reports what a listing
// shows. Run the sweep between the two invocations. Throwaway probe.
package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const withPart = "sources/shop/logical/01WITHPART/dump.zst.age"
const noPart = "sources/shop/logical/01NOPART/dump.zst.age"

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

	if len(os.Args) > 1 && os.Args[1] == "create" {
		for _, k := range []string{withPart, noPart} {
			cr, err := c.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
				Bucket: &b, Key: aws.String(k)})
			if err != nil {
				log.Fatal(err)
			}
			if k == withPart {
				if _, err := c.UploadPart(ctx, &s3.UploadPartInput{
					Bucket: &b, Key: aws.String(k), UploadId: cr.UploadId,
					PartNumber: aws.Int32(1),
					Body:       bytes.NewReader(bytes.Repeat([]byte("x"), 5<<20)),
				}); err != nil {
					log.Fatal(err)
				}
			}
		}
	}

	out, err := c.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: &b})
	if err != nil {
		log.Fatal(err)
	}
	seen := map[string]bool{}
	for _, u := range out.Uploads {
		seen[*u.Key] = true
	}
	fmt.Printf("  avec une part  : %v\n", seen[withPart])
	fmt.Printf("  sans aucune part: %v\n", seen[noPart])
}
