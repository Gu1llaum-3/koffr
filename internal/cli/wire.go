package cli

import (
	"context"
	"fmt"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/catalog/sqlite"
	"github.com/Gu1llaum-3/koffr/internal/config"
	"github.com/Gu1llaum-3/koffr/internal/crypto"
	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/executor/local"
	"github.com/Gu1llaum-3/koffr/internal/executor/ssh"
	"github.com/Gu1llaum-3/koffr/internal/source/postgres"
	"github.com/Gu1llaum-3/koffr/internal/storage"
	"github.com/Gu1llaum-3/koffr/internal/storage/fs"
	s3store "github.com/Gu1llaum-3/koffr/internal/storage/s3"
)

// This file turns a configuration into working objects, and is the only place
// that does. Keeping it in one file is what stops "which destination did this
// command open, and how" from becoming a question with several answers.

// destinationFor picks the destination a source writes to.
//
// Naming one explicitly is required as soon as a source has more than one: an
// operator restoring from the wrong repository has usually been helped there by
// a tool that picked for them.
func (a *app) destinationFor(cfg config.Config, src config.Source, want string) (string, config.Destination, error) {
	if want == "" {
		if len(src.Destinations) != 1 {
			return "", config.Destination{}, fault(ExitUsage,
				"this source writes to %d destinations (%v); name one with --destination",
				len(src.Destinations), src.Destinations)
		}
		want = src.Destinations[0]
	}
	dest, ok := cfg.Destinations[want]
	if !ok {
		return "", config.Destination{}, fault(ExitUsage,
			"no destination %q in %s", want, cfg.Path())
	}
	return want, dest, nil
}

// openStorage opens a repository.
func openStorage(ctx context.Context, dest config.Destination) (storage.Storage, error) {
	switch dest.Type {
	case "fs":
		st, err := fs.New(dest.Path)
		if err != nil {
			return nil, fmt.Errorf("destination %s: %w", dest.Path, err)
		}
		return st, nil
	case "s3":
		client, err := s3Client(ctx, dest)
		if err != nil {
			return nil, err
		}
		return s3store.New(ctx, client, s3store.Config{Bucket: dest.Bucket, Prefix: dest.Prefix})
	default:
		// Unreachable through Load, which rejects unknown types. Kept because
		// "unreachable" and "cannot happen" are different claims.
		return nil, fmt.Errorf("destination type %q is not supported", dest.Type)
	}
}

func s3Client(ctx context.Context, dest config.Destination) (*awss3.Client, error) {
	opts := []func(*awsconfig.LoadOptions) error{}
	if dest.Region != "" {
		opts = append(opts, awsconfig.WithRegion(dest.Region))
	}
	// Explicit keys win over the ambient chain. Left unset, the SDK finds
	// instance credentials, which is what running in EKS or on EC2 wants.
	if !dest.AccessKeyID.IsZero() {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				dest.AccessKeyID.Value(), dest.SecretAccessKey.Value(), "")))
	}
	base, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("s3 credentials: %w", err)
	}
	return awss3.NewFromConfig(base, func(o *awss3.Options) {
		if dest.Endpoint != "" {
			o.BaseEndpoint = &dest.Endpoint
			// Anything that is not AWS wants path style, and a MinIO or Ceph
			// endpoint is the reason to set an endpoint at all.
			o.UsePathStyle = true
		}
	}), nil
}

func openCatalog(ctx context.Context, cfg config.Config) (catalog.MetadataStore, error) {
	store, err := sqlite.Open(ctx, cfg.Catalog.Path)
	if err != nil {
		return nil, fmt.Errorf("catalog %s: %w", cfg.Catalog.Path, err)
	}
	return store, nil
}

func sealerFor(cfg config.Config) (crypto.Sealer, error) {
	s, err := crypto.NewSealer(cfg.Crypto.Recipients)
	if err != nil {
		return nil, &Fault{Code: ExitConfig, Err: err}
	}
	return s, nil
}

func openerFor(cfg config.Config) (crypto.Opener, error) {
	if cfg.Crypto.Identity.IsZero() {
		return nil, &Fault{Code: ExitConfig, Err: fmt.Errorf(
			"no crypto.identity in %s; reading a backup needs the identity, not just the recipients",
			cfg.Path())}
	}
	o, err := crypto.NewOpener(cfg.Crypto.Identity.Value())
	if err != nil {
		return nil, &Fault{Code: ExitConfig, Err: err}
	}
	return o, nil
}

// executorFor decides how Koffr reaches a source's database.
//
// Local when the database is reachable directly, an SSH tunnel when it is not.
// The tunnel does not get exec rights unless the configuration asked: a tunnel
// is a way through a firewall, and it should not quietly become a shell on the
// database host (CT-002).
func executorFor(ctx context.Context, src config.Source) (executor.Executor, error) {
	if src.SSH == nil {
		return local.New(), nil
	}
	ex, err := ssh.Dial(ctx, ssh.Config{
		Address:               src.SSH.Address,
		User:                  src.SSH.User,
		Password:              src.SSH.Password.Value(),
		PrivateKey:            []byte(src.SSH.PrivateKey.Value()),
		PrivateKeyPassword:    []byte(src.SSH.PrivateKeyPassword.Value()),
		KnownHostsFile:        src.SSH.KnownHostsFile,
		InsecureIgnoreHostKey: src.SSH.InsecureIgnoreHostKey,
		AllowExec:             src.SSH.AllowExec,
		Timeout:               30 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("ssh to %s: %w", src.SSH.Address, err)
	}
	return ex, nil
}

// postgresConfig is the shape both the source and the restore driver take.
func postgresConfig(src config.Source, toolRunner executor.Executor) postgres.Config {
	return postgres.Config{
		Host:           src.Host,
		Port:           src.Port,
		User:           src.User,
		Password:       src.Password.Value(),
		Database:       src.Database,
		SSLMode:        src.SSLMode,
		SSLRootCert:    src.SSLRootCert,
		BinDir:         src.BinDir,
		ToolRunner:     toolRunner,
		ConnectTimeout: 15 * time.Second,
	}
}
