// Does a killed job leave a multipart upload behind, and does anything see it?
//
// Creates a multipart upload, sends one part, and exits without completing or
// aborting -- exactly what a SIGKILL or a crashed host leaves on the service.
// Then lists the bucket the way an operator would, and lists the in-flight
// multipart uploads the way nothing in Koffr currently does. Throwaway probe.
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

func main() {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			os.Getenv("KOFFR_S3_KEY"), os.Getenv("KOFFR_S3_SECRET"), "")))
	if err != nil {
		log.Fatal(err)
	}
	c := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String("http://127.0.0.1:9030")
		o.UsePathStyle = true
	})
	bucket, key := "backups", "sources/shop/logical/01ABANDONED/dump.pgdump.zst.age"

	create, err := c.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: &bucket, Key: &key,
	})
	if err != nil {
		log.Fatal(err)
	}
	part := bytes.Repeat([]byte("x"), 8<<20)
	if _, err := c.UploadPart(ctx, &s3.UploadPartInput{
		Bucket: &bucket, Key: &key, UploadId: create.UploadId,
		PartNumber: aws.Int32(1), Body: bytes.NewReader(part),
	}); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  8 MiB envoyes, upload id %s\n", *create.UploadId)

	objs, err := c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: &bucket})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  ListObjectsV2 (ce que voit l'operateur) : %d objets\n", len(objs.Contents))

	ups, err := c.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: &bucket})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  ListMultipartUploads (ce que facture le service) : %d televersements\n", len(ups.Uploads))
	for _, u := range ups.Uploads {
		fmt.Printf("    %s  initie %s\n", *u.Key, u.Initiated.Format("15:04:05"))
	}
	// On sort sans CompleteMultipartUpload ni AbortMultipartUpload.
}
