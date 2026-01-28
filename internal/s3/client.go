package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"meme-video-gen/internal"
)

type Client interface {
	PutBytes(ctx context.Context, key string, b []byte, contentType string) error
	GetBytes(ctx context.Context, key string) ([]byte, string, error)
	GetReader(ctx context.Context, key string) (*ObjectReader, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)

	ReadJSON(ctx context.Context, key string, out any) (bool, error)
	WriteJSON(ctx context.Context, key string, v any) error
}

type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified *string
	ETag         string
}

type ObjectReader struct {
	Reader io.ReadCloser
	Size   int64
}

type s3Client struct {
	bucket string
	api    *awss3.Client
	upl    *manager.Uploader
	dl     *manager.Downloader
}

func New(cfg internal.Config) (Client, error) {
	endpoint := cfg.S3Endpoint
	forcePathStyle := true
	if strings.Contains(endpoint, "amazonaws.com") {
		forcePathStyle = false
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(cfg.S3Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.S3AccessKey, cfg.S3SecretKey, "")),
	)
	if err != nil {
		return nil, err
	}

	client := awss3.NewFromConfig(awsCfg, func(o *awss3.Options) {
		o.UsePathStyle = forcePathStyle
		o.BaseEndpoint = &endpoint
	})

	return &s3Client{
		bucket: cfg.S3Bucket,
		api:    client,
		upl:    manager.NewUploader(client),
		dl:     manager.NewDownloader(client),
	}, nil
}

func (c *s3Client) PutBytes(ctx context.Context, key string, b []byte, contentType string) error {
	_, err := c.api.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:      &c.bucket,
		Key:         &key,
		Body:        bytes.NewReader(b),
		ContentType: &contentType,
	})
	return err
}

func (c *s3Client) GetBytes(ctx context.Context, key string) ([]byte, string, error) {
	out, err := c.api.GetObject(ctx, &awss3.GetObjectInput{Bucket: &c.bucket, Key: &key})
	if err != nil {
		var noSuchKey *types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return nil, "", osErrNotExist(err)
		}
		return nil, "", err
	}
	defer out.Body.Close()
	b, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, "", err
	}
	ct := ""
	if out.ContentType != nil {
		ct = *out.ContentType
	}
	return b, ct, nil
}

func (c *s3Client) GetReader(ctx context.Context, key string) (*ObjectReader, error) {
	out, err := c.api.GetObject(ctx, &awss3.GetObjectInput{Bucket: &c.bucket, Key: &key})
	if err != nil {
		var noSuchKey *types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return nil, osErrNotExist(err)
		}
		return nil, err
	}

	size := int64(0)
	if out.ContentLength != nil {
		size = *out.ContentLength
	}

	return &ObjectReader{
		Reader: out.Body,
		Size:   size,
	}, nil
}

func (c *s3Client) Delete(ctx context.Context, key string) error {
	_, err := c.api.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: &c.bucket, Key: &key})
	return err
}

func (c *s3Client) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	var out []ObjectInfo
	p := awss3.NewListObjectsV2Paginator(c.api, &awss3.ListObjectsV2Input{Bucket: &c.bucket, Prefix: &prefix})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Contents {
			lm := ""
			if obj.LastModified != nil {
				lm = obj.LastModified.Format("2006-01-02T15:04:05Z07:00")
			}
			sz := int64(0)
			if obj.Size != nil {
				sz = *obj.Size
			}
			out = append(out, ObjectInfo{Key: *obj.Key, Size: sz, LastModified: &lm, ETag: deref(obj.ETag)})
		}
	}
	return out, nil
}

func (c *s3Client) ReadJSON(ctx context.Context, key string, out any) (bool, error) {
	b, _, err := c.GetBytes(ctx, key)
	if err != nil {
		if isNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, json.Unmarshal(b, out)
}

func (c *s3Client) WriteJSON(ctx context.Context, key string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return c.PutBytes(ctx, key, b, "application/json")
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func isNotExist(err error) bool {
	return errors.Is(err, errNotExist)
}

var errNotExist = errors.New("not exist")

func osErrNotExist(err error) error {
	_ = err
	return errNotExist
}

func mustParseURL(s string) *url.URL {
	u, _ := url.Parse(s)
	return u
}
