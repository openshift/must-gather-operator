/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package s3upload implements the "s3-upload" subcommand of the operator binary.
// The upload container (build/bin/upload) calls it to copy the must-gather
// archive to an S3-compatible bucket configured through spec.storage.s3.
package s3upload

import (
	"context"
	goflag "flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// DefaultRegion is used when no region is configured. Most S3-compatible
// stores (ODF/NooBaa, Ceph RGW, MinIO) accept it regardless of location.
const DefaultRegion = "us-east-1"

// Options holds the parameters of a single archive upload.
type Options struct {
	// File is the local path of the archive to upload.
	File string
	// Bucket is the destination bucket.
	Bucket string
	// KeyPrefix is prepended to the file base name to build the object key.
	KeyPrefix string
	// Region of the bucket. Defaults to DefaultRegion.
	Region string
	// Endpoint is an optional S3-compatible endpoint URL. When set,
	// path-style addressing is used.
	Endpoint string
}

// ObjectKey returns the object key for file under prefix: "<prefix>/<basename>".
// Leading and trailing slashes on prefix are ignored.
func ObjectKey(prefix, file string) string {
	name := filepath.Base(file)
	p := strings.Trim(prefix, "/")
	if p == "" {
		return name
	}
	return p + "/" + name
}

// newS3Client builds an S3 client. Credentials come from the default AWS
// chain, which reads AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY from the
// environment (injected from the CR's credentials Secret). The default HTTP
// client honors HTTP(S)_PROXY / NO_PROXY and the system trust store, where
// the operator mounts the cluster trusted CA bundle.
func newS3Client(ctx context.Context, o Options) (*s3.Client, error) {
	region := o.Region
	if region == "" {
		region = DefaultRegion
	}

	loadOpts := []func(*config.LoadOptions) error{config.WithRegion(region)}
	if o.Endpoint != "" {
		// Many S3-compatible stores reject the flexible checksums the SDK
		// sends by default; only send them when the operation requires it.
		loadOpts = append(loadOpts,
			config.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
			config.WithResponseChecksumValidation(aws.ResponseChecksumValidationWhenRequired),
		)
	}

	cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS configuration: %w", err)
	}

	return s3.NewFromConfig(cfg, func(so *s3.Options) {
		if o.Endpoint != "" {
			so.BaseEndpoint = aws.String(o.Endpoint)
			so.UsePathStyle = true
		}
	}), nil
}

// Upload copies o.File to s3://o.Bucket/ObjectKey(o.KeyPrefix, o.File).
// Files larger than the transfer manager threshold use multipart upload.
// It returns the object key written.
func Upload(ctx context.Context, o Options) (string, error) {
	if o.File == "" || o.Bucket == "" {
		return "", fmt.Errorf("file and bucket are required")
	}

	f, err := os.Open(o.File)
	if err != nil {
		return "", fmt.Errorf("failed to open %s: %w", o.File, err)
	}
	defer f.Close()

	client, err := newS3Client(ctx, o)
	if err != nil {
		return "", err
	}

	key := ObjectKey(o.KeyPrefix, o.File)
	// *os.File is an io.Seeker, so the transfer manager knows the size up front.
	if _, err := transfermanager.New(client).UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket:      aws.String(o.Bucket),
		Key:         aws.String(key),
		Body:        f,
		ContentType: aws.String("application/gzip"),
	}); err != nil {
		return "", fmt.Errorf("failed to upload to s3://%s/%s: %w", o.Bucket, key, err)
	}
	return key, nil
}

// RunS3Upload executes the s3-upload subcommand. Args are everything after
// "s3-upload" on the command line. Returns an exit code suitable for os.Exit.
// Credentials are never accepted as flags so they do not show up in the
// process list; they are read from the environment by the AWS SDK.
func RunS3Upload(args []string) int {
	fs := goflag.NewFlagSet("s3-upload", goflag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var o Options
	fs.StringVar(&o.File, "file", "", "path of the archive to upload")
	fs.StringVar(&o.Bucket, "bucket", "", "destination bucket")
	fs.StringVar(&o.KeyPrefix, "key-prefix", "", "optional prefix for the object key")
	fs.StringVar(&o.Region, "region", "", "bucket region (default "+DefaultRegion+")")
	fs.StringVar(&o.Endpoint, "endpoint", "", "optional S3-compatible endpoint URL (enables path-style addressing)")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse s3-upload flags: %v\n", err)
		return 1
	}
	if o.File == "" || o.Bucket == "" {
		fmt.Fprintln(os.Stderr, "s3-upload requires --file and --bucket")
		return 1
	}

	key, err := Upload(context.Background(), o)
	if err != nil {
		fmt.Fprintf(os.Stderr, "s3 upload failed: %v\n", err)
		return 1
	}
	fmt.Printf("Successfully uploaded %s to s3://%s/%s\n", filepath.Base(o.File), o.Bucket, key)
	return 0
}
