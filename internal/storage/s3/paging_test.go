package s3_test

import (
	"fmt"
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

// pages replies to ListMultipartUploads with a canned, truncated listing.
//
// MinIO cannot produce one: measured, it ignores MaxUploads, never sets
// IsTruncated and never returns a marker, whatever it is asked for. So the
// paging branch is unreachable against the container the rest of this suite
// uses, and reachable against AWS, which caps a listing at a thousand. A store
// that quietly stopped at the first page would look healthy -- uploads
// returned, no error -- while leaving everything after it paid for. That is
// worth a stub rather than leaving the branch to be exercised in production.
type pages struct {
	seen []string // the marker pair each request carried, in order
}

func (p *pages) Do(req *http.Request) (*http.Response, error) {
	q := req.URL.Query()
	// Each candidate is checked for ownership with ListParts, which is a
	// different request: it carries an upload id rather than the uploads flag.
	if q.Has("uploadId") {
		// Split across two pages as well. An upload big enough to be worth
		// finding is an upload with more than a thousand parts, and a size
		// that stopped at the first page would understate what is being paid
		// for -- quietly, and always in the reassuring direction.
		body := `<?xml version="1.0" encoding="UTF-8"?>
<ListPartsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <IsTruncated>true</IsTruncated>
  <NextPartNumberMarker>1</NextPartNumberMarker>
  <Part><PartNumber>1</PartNumber><Size>5242880</Size></Part>
</ListPartsResult>`
		if q.Get("part-number-marker") != "" {
			body = `<?xml version="1.0" encoding="UTF-8"?>
<ListPartsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <IsTruncated>false</IsTruncated>
  <Part><PartNumber>2</PartNumber><Size>3145728</Size></Part>
</ListPartsResult>`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/xml"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	}
	// New() asks the bucket about Object Lock and versioning on the way up.
	// Those are not this stub's business: answering them with a listing would
	// count them as pages and hide the very thing under test.
	if _, ok := q["uploads"]; !ok {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	}
	p.seen = append(p.seen, q.Get("key-marker")+"/"+q.Get("upload-id-marker"))

	var body string
	switch q.Get("key-marker") {
	case "":
		body = listing(true, "sources/a/dump", "upload-1",
			upload("sources/a/dump", "upload-1"))
	default:
		body = listing(false, "", "",
			upload("sources/b/dump", "upload-2"),
			upload("sources/b/dump", "upload-3"))
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/xml"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func upload(key, id string) string {
	return fmt.Sprintf(
		"<Upload><Key>%s</Key><UploadId>%s</UploadId>"+
			"<Initiated>2026-09-06T10:00:00.000Z</Initiated></Upload>", key, id)
}

func listing(truncated bool, nextKey, nextUpload string, uploads ...string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<ListMultipartUploadsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Bucket>koffr</Bucket>
  <IsTruncated>%t</IsTruncated>
  <NextKeyMarker>%s</NextKeyMarker>
  <NextUploadIdMarker>%s</NextUploadIdMarker>
  %s
</ListMultipartUploadsResult>`, truncated, nextKey, nextUpload, strings.Join(uploads, "\n  "))
}

func TestIncompleteUploadsFollowPages(t *testing.T) {
	stub := &pages{}
	client := awss3.New(awss3.Options{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("k", "s", ""),
		HTTPClient:  stub,
		// A listing is a GET; without this the SDK would still reach for a
		// real endpoint to sign against.
		BaseEndpoint: aws.String("https://s3.example.invalid"),
		UsePathStyle: true,
	})
	st, err := koffrs3.New(t.Context(), client, koffrs3.Config{Bucket: "koffr"})
	require.NoError(t, err)

	var base storage.Storage = st
	found, err := base.(storage.MultipartMaintainer).ListIncompleteUploads(t.Context(), "sources/")
	require.NoError(t, err)

	require.Len(t, found, 3, "the sweep stopped at a page boundary")
	require.Equal(t, "sources/b/dump", found[2].Key)
	require.Equal(t, "upload-3", found[2].UploadID)
	require.Equal(t, int64(5<<20+3<<20), found[2].Bytes, "the size stopped at the first page of parts")

	// Both markers have to be carried forward. One key can hold several
	// unfinished uploads, so advancing on the key alone asks for the same page
	// again, for ever.
	require.Equal(t, []string{"/", "sources/a/dump/upload-1"}, stub.seen)
}
