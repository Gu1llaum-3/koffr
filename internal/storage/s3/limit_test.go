package s3_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/storage"
	koffrs3 "github.com/Gu1llaum-3/koffr/internal/storage/s3"
)

// refusesParts answers CreateMultipartUpload and then fails every part with the
// wording the upload manager uses when it runs out of part numbers.
//
// Reaching the real limit costs ten thousand parts, so it is not something a
// test can provoke. What matters is what an operator is told when it happens,
// and that is reachable: the manager's own message names MaxUploadParts and
// tells them to "adjust PartSize", which is not a setting Koffr exposes under
// that name -- so left alone it sends them looking for a knob that is not there.
type refusesParts struct{}

func (refusesParts) Do(req *http.Request) (*http.Response, error) {
	q := req.URL.Query()
	switch {
	case q.Has("uploads"):
		return xml(req, http.StatusOK, `<?xml version="1.0" encoding="UTF-8"?>
<InitiateMultipartUploadResult><Bucket>koffr</Bucket><Key>k</Key>
<UploadId>u1</UploadId></InitiateMultipartUploadResult>`)
	case q.Has("partNumber"), q.Has("uploadId"):
		return xml(req, http.StatusInternalServerError, `<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>InternalError</Code><Message>exceeded total allowed S3 limit MaxUploadParts (10000). `+
			`Adjust PartSize to fit in this limit</Message></Error>`)
	}
	return xml(req, http.StatusNotFound, "")
}

func xml(req *http.Request, code int, body string) (*http.Response, error) {
	return &http.Response{
		StatusCode: code,
		Header:     http.Header{"Content-Type": []string{"application/xml"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func TestPut_SaysWhatToDoWhenAnArtifactOutgrowsItsPartSize(t *testing.T) {
	client := awss3.New(awss3.Options{
		Region:           "us-east-1",
		Credentials:      credentials.NewStaticCredentialsProvider("k", "s", ""),
		HTTPClient:       refusesParts{},
		BaseEndpoint:     aws.String("https://s3.example.invalid"),
		UsePathStyle:     true,
		RetryMaxAttempts: 1,
	})
	st, err := koffrs3.New(t.Context(), client, koffrs3.Config{
		Bucket: "koffr", PartSize: 16 << 20,
	})
	require.NoError(t, err)

	// Larger than one part, or the manager would send a plain PutObject and
	// never reach the multipart path this is about.
	_, err = st.Put(t.Context(), "sources/prod/logical/01BIG/dump.zst.age",
		strings.NewReader(strings.Repeat("x", 20<<20)), storage.PutOptions{})
	require.Error(t, err)

	msg := err.Error()
	require.Contains(t, msg, "part_size_mib", "the operator has to be told the name of the setting")
	require.Contains(t, msg, "156 GiB", "and the ceiling they just hit: 16 MiB times ten thousand parts")
}

// The stub above covers the wording. This covers the thing itself: a real
// service, a real multipart upload, and a part limit it actually runs into.
//
// It matters that the refusal is ours. Left to the provider, an upload past
// its limit fails with the provider's own error -- which our translation would
// not recognise, so the operator would get a raw S3 message at the end of an
// upload that had already run for hours. Enforcing the configured limit makes
// the failure deterministic and legible on every backend, including the ones
// that cap at a thousand parts.
func TestPut_EnforcesTheConfiguredPartLimit(t *testing.T) {
	if shared.skipWhy != "" {
		t.Skip(shared.skipWhy)
	}
	st, err := koffrs3.New(t.Context(), shared.client, koffrs3.Config{
		Bucket:   newBucket(t),
		PartSize: 5 << 20,
		MaxParts: 3,
	})
	require.NoError(t, err)

	_, err = st.Put(t.Context(), "sources/prod/logical/01BIG/dump.zst.age",
		strings.NewReader(strings.Repeat("x", 20<<20)), storage.PutOptions{})
	require.Error(t, err)

	msg := err.Error()
	require.Contains(t, msg, "15 MiB", "5 MiB parts, three of them")
	require.Contains(t, msg, "max_parts")
	require.Contains(t, msg, "part_size_mib")
}

// A store is built from a configuration that has already been validated, but it
// can also be built directly -- by a test, by another caller -- and a part limit
// outside what S3 accepts would be discovered as a failed upload rather than as
// a refused setup (PD-006).
func TestNew_RefusesAPartLimitS3WouldNotAccept(t *testing.T) {
	for _, n := range []int{-1, 10001} {
		_, err := koffrs3.New(t.Context(), shared.client, koffrs3.Config{
			Bucket: "koffr", MaxParts: n,
		})
		require.Error(t, err, "max parts %d", n)
		require.Contains(t, err.Error(), "max parts")
	}
}
