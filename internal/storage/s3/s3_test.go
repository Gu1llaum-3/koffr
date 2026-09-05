package s3_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/Gu1llaum-3/koffr/internal/storage"
	koffrs3 "github.com/Gu1llaum-3/koffr/internal/storage/s3"
	"github.com/Gu1llaum-3/koffr/internal/storage/storagetest"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

// One MinIO container for the whole package: starting it costs seconds, and
// each test gets its own bucket, which is isolation enough.
var shared struct {
	client   *awss3.Client
	skipWhy  string
	teardown func()
}

func TestMain(m *testing.M) {
	code := func() int {
		ctx := context.Background()

		if why := testutil.EnsureDockerHost(); why != "" {
			shared.skipWhy = why
		}
		container, err := minio.Run(ctx, "minio/minio:RELEASE.2025-04-22T22-12-26Z")
		if err != nil {
			shared.skipWhy = fmt.Sprintf("MinIO container unavailable: %v", err)
		}
		// Locally a missing daemon is a skip; under CI it is a failure, because
		// a silently skipped S3 suite is indistinguishable from a passing one.
		if _, fatal := testutil.SkipOrFailWithoutDocker(shared.skipWhy); fatal != "" {
			fmt.Fprintln(os.Stderr, fatal)
			return 1
		}
		if shared.skipWhy != "" {
			return m.Run()
		}
		shared.teardown = func() {
			if err := testcontainers.TerminateContainer(container); err != nil {
				fmt.Fprintf(os.Stderr, "terminate MinIO: %v\n", err)
			}
		}
		defer shared.teardown()

		endpoint, err := container.ConnectionString(ctx)
		if err != nil {
			shared.skipWhy = fmt.Sprintf("MinIO endpoint unavailable: %v", err)
			return m.Run()
		}

		cfg, err := awsconfig.LoadDefaultConfig(ctx,
			awsconfig.WithRegion("us-east-1"),
			awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
				container.Username, container.Password, "")),
		)
		if err != nil {
			shared.skipWhy = fmt.Sprintf("AWS config: %v", err)
			return m.Run()
		}
		shared.client = awss3.NewFromConfig(cfg, func(o *awss3.Options) {
			o.BaseEndpoint = aws.String("http://" + endpoint)
			// MinIO serves buckets as paths, not as subdomains.
			o.UsePathStyle = true
		})
		return m.Run()
	}()
	os.Exit(code)
}

// newBucket gives each test its own bucket, so a listing in one cannot see
// another's objects.
func newBucket(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("koffr-%d-%d", time.Now().UnixNano(), len(t.Name()))
	_, err := shared.client.CreateBucket(t.Context(), &awss3.CreateBucketInput{
		Bucket: aws.String(name),
	})
	require.NoError(t, err)
	return name
}

// The identical suite that storage/fs runs. Not one test is rewritten, which is
// the whole return on writing the contract before the first implementation.
func TestContract(t *testing.T) {
	if shared.skipWhy != "" {
		t.Skip(shared.skipWhy)
	}
	storagetest.Suite(t, func(t *testing.T) storage.Storage {
		s, err := koffrs3.New(t.Context(), shared.client, koffrs3.Config{
			Bucket: newBucket(t),
		})
		require.NoError(t, err)
		return s
	})
}

// A repository may share a bucket with other things, so the prefix must be
// applied on the way in and stripped on the way out. A listing that leaked the
// prefix would produce keys that layout.Parse cannot read.
func TestContract_WithPrefix(t *testing.T) {
	if shared.skipWhy != "" {
		t.Skip(shared.skipWhy)
	}
	bucket := newBucket(t)
	storagetest.Suite(t, func(t *testing.T) storage.Storage {
		// A fresh sub-prefix per test keeps the isolation the suite assumes
		// while still exercising the prefix mapping.
		s, err := koffrs3.New(t.Context(), shared.client, koffrs3.Config{
			Bucket: bucket,
			Prefix: fmt.Sprintf("repos/%d", time.Now().UnixNano()),
		})
		require.NoError(t, err)
		return s
	})
}

// Object Lock is off on these buckets, so the capability must say so and Put
// must refuse rather than quietly store a deletable object (EF-042).
func TestImmutabilityIsNotClaimedFalsely(t *testing.T) {
	if shared.skipWhy != "" {
		t.Skip(shared.skipWhy)
	}
	s, err := koffrs3.New(t.Context(), shared.client, koffrs3.Config{Bucket: newBucket(t)})
	require.NoError(t, err)
	require.False(t, s.Capabilities().Immutable,
		"a bucket without Object Lock must not advertise immutability")
}
