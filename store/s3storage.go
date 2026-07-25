package store

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"maps"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func init() {
	// Register the s3 URI scheme
	RegisterURIScheme("s3", S3StorageFactory)
}

// S3Storage implements the Storage interface for S3
type S3Storage struct {
	client   *s3.Client
	region   string
	endpoint string
	metrics  MetricsCollector
	tracer   TracingProvider
	logger   Logger
}

// S3StorageOptions defines options for S3Storage
type S3StorageOptions struct {
	Region       Field[string]
	Endpoint     Field[string]
	AccessKey    Field[string]
	SecretKey    Field[string]
	SessionToken Field[string]
	Profile      Field[string]
	ObservabilityOptions
}

// S3StorageOption configures S3Storage
type S3StorageOption interface {
	applyS3Storage(*S3StorageOptions)
}

// S3StorageOptionFunc implements S3StorageOption as a function
type S3StorageOptionFunc func(*S3StorageOptions)

func (f S3StorageOptionFunc) applyS3Storage(opts *S3StorageOptions) {
	f(opts)
}

// WithS3Region sets the AWS region
func WithS3Region(region string) S3StorageOption {
	return S3StorageOptionFunc(func(opts *S3StorageOptions) {
		opts.Region.Set(region)
	})
}

// WithS3Endpoint sets a custom endpoint
func WithS3Endpoint(endpoint string) S3StorageOption {
	return S3StorageOptionFunc(func(opts *S3StorageOptions) {
		opts.Endpoint.Set(endpoint)
	})
}

// WithS3Credentials sets AWS credentials
func WithS3Credentials(accessKey, secretKey string) S3StorageOption {
	return S3StorageOptionFunc(func(opts *S3StorageOptions) {
		opts.AccessKey.Set(accessKey)
		opts.SecretKey.Set(secretKey)
	})
}

// WithS3SessionToken sets a session token for temporary credentials
func WithS3SessionToken(token string) S3StorageOption {
	return S3StorageOptionFunc(func(opts *S3StorageOptions) {
		opts.SessionToken.Set(token)
	})
}

func WithS3Profile(profile string) S3StorageOption {
	return S3StorageOptionFunc(func(opts *S3StorageOptions) {
		opts.Profile.Set(profile)
	})
}

// NewS3Storage creates a new S3Storage
func NewS3Storage(options ...S3StorageOption) (*S3Storage, error) {
	// Create options with defaults
	opts := &S3StorageOptions{}

	// Apply provided options
	for _, option := range options {
		option.applyS3Storage(opts)
	}

	// Configure AWS SDK
	var awsConfig aws.Config
	var err error

	configOptions := []func(*config.LoadOptions) error{
		config.WithRegion(opts.Region.Or("us-east-1")),
	}

	// Add custom endpoint if provided
	if opts.Endpoint.IsSet() {
		configOptions = append(configOptions, config.WithBaseEndpoint(opts.Endpoint.Get()))
	}

	// Add credentials if provided
	if opts.AccessKey.IsSet() && opts.SecretKey.IsSet() {
		creds := credentials.NewStaticCredentialsProvider(
			opts.AccessKey.Get(),
			opts.SecretKey.Get(),
			opts.SessionToken.Or(""),
		)
		configOptions = append(configOptions, config.WithCredentialsProvider(creds))
	}

	// Load the configuration
	awsConfig, err = config.LoadDefaultConfig(
		context.Background(),
		configOptions...,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create S3 client
	s3Client := s3.NewFromConfig(awsConfig)

	return &S3Storage{
		client:   s3Client,
		region:   opts.Region.Or("us-east-1"),
		endpoint: opts.Endpoint.Or(""),
		metrics:  opts.Metrics.Or(&NoOpMetrics{}),
		tracer:   opts.Tracer.Or(&NoOpTracer{}),
		logger:   opts.Logger.Or(&NoOpLogger{}),
	}, nil
}

// URIScheme returns the URI scheme for S3 storage
func (s *S3Storage) URIScheme() string {
	return "s3"
}

// HasLinearizableConditions reports support for AWS S3 single-key conditional
// writes and deletes. Custom S3-compatible endpoints must implement the same
// semantics to be safe for Locker.
func (s *S3Storage) HasLinearizableConditions() bool {
	return true
}

// Get retrieves an object from S3
func (s *S3Storage) Get(ctx context.Context, uri string, options ...GetOption) ([]byte, Metadata, error) {
	span := s.tracer.StartSpan(ctx, "S3Storage.Get")
	defer span.End()

	start := time.Now()
	defer func() {
		s.metrics.RecordDuration("storage.s3.get.duration", time.Since(start))
	}()

	rd, md, err := s.GetStream(ctx, uri, options...)
	if err != nil {
		return nil, Metadata{}, err
	}

	// Read the data
	data, err := io.ReadAll(rd)
	if err != nil {
		return nil, md, NewStoreError(
			ErrCodeIO,
			"Get",
			uri,
			"Failed to read object data",
			err,
			true,
		)
	}
	// Close the stream
	if err := rd.Close(); err != nil {
		return nil, md, NewStoreError(
			ErrCodeIO,
			"Get",
			uri,
			"Failed to close object stream",
			err,
			true,
		)
	}

	s.metrics.RecordSize("storage.s3.get.bytes", md.Size)

	return data, md, nil
}

// GetStream returns an object as a stream
func (s *S3Storage) GetStream(ctx context.Context, uri string, options ...GetOption) (io.ReadCloser, Metadata, error) {
	span := s.tracer.StartSpan(ctx, "S3Storage.GetStream")
	defer span.End()

	start := time.Now()
	defer func() {
		s.metrics.RecordDuration("storage.s3.getstream.duration", time.Since(start))
	}()

	// Parse the URI
	parsedURI, err := ParseURI(uri)
	if err != nil {
		return nil, Metadata{}, NewInvalidURIError("GetStream", uri, err)
	}

	// Apply options
	opts := &OperationOptions{}
	applyGetOptions(opts, options)

	// Build get object input
	input := &s3.GetObjectInput{
		Bucket: aws.String(parsedURI.Bucket),
		Key:    aws.String(parsedURI.Key),
	}

	// Apply conditional options
	if opts.IfMatch.IsRight() {
		input.IfMatch = aws.String(opts.IfMatch.Right())
	}

	if opts.IfNoMatch.IsRight() {
		input.IfNoneMatch = aws.String(opts.IfNoMatch.Right())
	}

	if opts.IfModified.IsRight() {
		input.IfModifiedSince = aws.Time(opts.IfModified.Right())
	}

	if opts.IfNotModified.IsRight() {
		input.IfUnmodifiedSince = aws.Time(opts.IfNotModified.Right())
	}

	// Get the object
	result, err := s.client.GetObject(ctx, input)
	if err != nil {
		return nil, Metadata{}, TranslateAWSError("GetStream", parsedURI.Key, err)
	}

	// Create metadata from result
	metadata := Metadata{
		ETag:            strings.Trim(aws.ToString(result.ETag), "\""),
		LastModified:    aws.ToTime(result.LastModified),
		Size:            aws.ToInt64(result.ContentLength),
		ContentType:     aws.ToString(result.ContentType),
		ContentEncoding: aws.ToString(result.ContentEncoding),
		StorageClass:    string(result.StorageClass),
		VersionID:       aws.ToString(result.VersionId),
		UserMetadata:    make(map[string]string),
	}

	// Copy metadata
	maps.Copy(metadata.UserMetadata, result.Metadata)
	return &safeReadCloser{
		Reader: result.Body,
		Closer: result.Body,
	}, metadata, nil
}

// Put stores an object in S3
func (s *S3Storage) Put(ctx context.Context, uri string, data []byte, options ...PutOption) (Metadata, error) {
	span := s.tracer.StartSpan(ctx, "S3Storage.Put")
	defer span.End()

	start := time.Now()
	defer func() {
		s.metrics.RecordDuration("storage.s3.put.duration", time.Since(start))
	}()

	// Parse the URI
	parsedURI, err := ParseURI(uri)
	if err != nil {
		return Metadata{}, NewInvalidURIError("Put", uri, err)
	}

	// Apply options
	opts := &OperationOptions{}
	applyPutOptions(opts, options)

	// Build put object input
	input := &s3.PutObjectInput{
		Bucket: aws.String(parsedURI.Bucket),
		Key:    aws.String(parsedURI.Key),
		Body:   bytes.NewReader(data),
	}

	// Set conditional options
	if wantsIfMatchExists(&opts.ConditionalOptions) {
		// S3 PutObject does not accept If-Match:* for existence-only checks.
		// Existence-only Put requires an ETag via IfMatch(etag).
		return Metadata{}, NewStoreError(ErrCodeUnsupported, "Put", parsedURI.Key, "IfExists on S3 Put requires IfMatch(etag)", nil, false)
	}
	if opts.IfMatch.IsRight() {
		input.IfMatch = aws.String(opts.IfMatch.Right())
	}

	if wantsIfNoneMatchStar(&opts.ConditionalOptions) {
		input.IfNoneMatch = aws.String("*")
	} else if opts.IfNoMatch.IsRight() {
		input.IfNoneMatch = aws.String(opts.IfNoMatch.Right())
	}

	// Set metadata options
	if opts.ContentType.IsSet() {
		input.ContentType = aws.String(opts.ContentType.Get())
	}

	if opts.ContentEncoding.IsSet() {
		input.ContentEncoding = aws.String(opts.ContentEncoding.Get())
	}

	if opts.StorageClass.IsSet() {
		input.StorageClass = types.StorageClass(opts.StorageClass.Get())
	}

	// Set user metadata
	if opts.Metadata.IsSet() {
		input.Metadata = opts.Metadata.Get()
	}

	// Put the object
	s.metrics.RecordSize("storage.s3.put.bytes", int64(len(data)))

	result, err := s.client.PutObject(ctx, input)
	if err != nil {
		return Metadata{}, TranslateAWSError("Put", parsedURI.Key, err)
	}

	// Create metadata from result
	metadata := Metadata{
		ETag:            strings.Trim(aws.ToString(result.ETag), "\""),
		LastModified:    time.Now(),
		Size:            int64(len(data)),
		ContentType:     opts.ContentType.Get(),
		ContentEncoding: opts.ContentEncoding.Get(),
		StorageClass:    opts.StorageClass.Get(),
		VersionID:       aws.ToString(result.VersionId),
		UserMetadata:    opts.Metadata.Cloned(),
	}

	return metadata, nil
}

// PutStream stores a stream in S3
func (s *S3Storage) PutStream(ctx context.Context, uri string, reader io.Reader, options ...PutOption) (Metadata, error) {
	span := s.tracer.StartSpan(ctx, "S3Storage.PutStream")
	defer span.End()

	start := time.Now()
	defer func() {
		s.metrics.RecordDuration("storage.s3.putstream.duration", time.Since(start))
	}()

	// Parse the URI
	parsedURI, err := ParseURI(uri)
	if err != nil {
		return Metadata{}, NewInvalidURIError("PutStream", uri, err)
	}

	// Apply options
	opts := &OperationOptions{}
	applyPutOptions(opts, options)

	// Build put object input
	input := &s3.PutObjectInput{
		Bucket: aws.String(parsedURI.Bucket),
		Key:    aws.String(parsedURI.Key),
		Body:   reader,
	}

	// Set conditional options
	if wantsIfMatchExists(&opts.ConditionalOptions) {
		// S3 PutObject does not accept If-Match:* for existence-only checks.
		// Existence-only Put requires an ETag via IfMatch(etag).
		return Metadata{}, NewStoreError(ErrCodeUnsupported, "PutStream", parsedURI.Key, "IfExists on S3 Put requires IfMatch(etag)", nil, false)
	}
	if opts.IfMatch.IsRight() {
		input.IfMatch = aws.String(opts.IfMatch.Right())
	}

	if wantsIfNoneMatchStar(&opts.ConditionalOptions) {
		input.IfNoneMatch = aws.String("*")
	} else if opts.IfNoMatch.IsRight() {
		input.IfNoneMatch = aws.String(opts.IfNoMatch.Right())
	}

	// Set metadata options
	if opts.ContentType.IsSet() {
		input.ContentType = aws.String(opts.ContentType.Get())
	}

	if opts.ContentEncoding.IsSet() {
		input.ContentEncoding = aws.String(opts.ContentEncoding.Get())
	}

	if opts.StorageClass.IsSet() {
		input.StorageClass = types.StorageClass(opts.StorageClass.Get())
	}

	// Set user metadata
	if opts.Metadata.IsSet() {
		input.Metadata = opts.Metadata.Get()
	}

	// Put the object
	result, err := s.client.PutObject(ctx, input)
	if err != nil {
		return Metadata{}, TranslateAWSError("PutStream", parsedURI.Key, err)
	}

	// Create metadata from result
	metadata := Metadata{
		ETag:            strings.Trim(aws.ToString(result.ETag), "\""),
		LastModified:    time.Now(),
		ContentType:     opts.ContentType.Get(),
		ContentEncoding: opts.ContentEncoding.Get(),
		StorageClass:    opts.StorageClass.Get(),
		VersionID:       aws.ToString(result.VersionId),
		UserMetadata:    opts.Metadata.Cloned(),
	}

	return metadata, nil
}

// Delete removes an object from S3
func (s *S3Storage) Delete(ctx context.Context, uri string, options ...DeleteOption) error {
	span := s.tracer.StartSpan(ctx, "S3Storage.Delete")
	defer span.End()

	start := time.Now()
	defer func() {
		s.metrics.RecordDuration("storage.s3.delete.duration", time.Since(start))
	}()

	// Parse the URI
	parsedURI, err := ParseURI(uri)
	if err != nil {
		return NewInvalidURIError("Delete", uri, err)
	}

	// Apply options
	opts := &OperationOptions{}
	applyDeleteOptions(opts, options)

	// Build delete object input
	input := &s3.DeleteObjectInput{
		Bucket: aws.String(parsedURI.Bucket),
		Key:    aws.String(parsedURI.Key),
	}

	// Apply conditional options
	if opts.IfMatch.IsRight() {
		input.IfMatch = aws.String(opts.IfMatch.Right())
	}

	// Set version ID if provided
	if opts.VersionID.IsSet() {
		input.VersionId = aws.String(opts.VersionID.Get())
	}

	// Delete the object
	_, err = s.client.DeleteObject(ctx, input)
	if err != nil {
		return TranslateAWSError("Delete", parsedURI.Key, err)
	}

	return nil
}

// Head retrieves metadata for an object
func (s *S3Storage) Head(ctx context.Context, uri string, options ...HeadOption) (Metadata, error) {
	span := s.tracer.StartSpan(ctx, "S3Storage.Head")
	defer span.End()

	start := time.Now()
	defer func() {
		s.metrics.RecordDuration("storage.s3.head.duration", time.Since(start))
	}()

	// Parse the URI
	parsedURI, err := ParseURI(uri)
	if err != nil {
		return Metadata{}, NewInvalidURIError("Head", uri, err)
	}

	// Apply options
	opts := &OperationOptions{}
	applyHeadOptions(opts, options)

	// Build head object input
	input := &s3.HeadObjectInput{
		Bucket: aws.String(parsedURI.Bucket),
		Key:    aws.String(parsedURI.Key),
	}

	// Apply conditional options
	if opts.IfMatch.IsRight() {
		input.IfMatch = aws.String(opts.IfMatch.Right())
	}

	if opts.IfNoMatch.IsRight() {
		input.IfNoneMatch = aws.String(opts.IfNoMatch.Right())
	}

	if opts.IfModified.IsRight() {
		input.IfModifiedSince = aws.Time(opts.IfModified.Right())
	}

	if opts.IfNotModified.IsRight() {
		input.IfUnmodifiedSince = aws.Time(opts.IfNotModified.Right())
	}

	// Set version ID if provided
	if opts.VersionID.IsSet() {
		input.VersionId = aws.String(opts.VersionID.Get())
	}

	// Head the object
	result, err := s.client.HeadObject(ctx, input)
	if err != nil {
		return Metadata{}, TranslateAWSError("Head", parsedURI.Key, err)
	}

	// Create metadata from result
	metadata := Metadata{
		ETag:            strings.Trim(aws.ToString(result.ETag), "\""),
		LastModified:    aws.ToTime(result.LastModified),
		Size:            aws.ToInt64(result.ContentLength),
		ContentType:     aws.ToString(result.ContentType),
		ContentEncoding: aws.ToString(result.ContentEncoding),
		StorageClass:    string(result.StorageClass),
		VersionID:       aws.ToString(result.VersionId),
		UserMetadata:    make(map[string]string),
	}

	// Copy metadata
	for k, v := range result.Metadata {
		metadata.UserMetadata[k] = v
	}

	return metadata, nil
}

// List returns objects matching specified criteria
func (s *S3Storage) List(ctx context.Context, uri string, options ...ListOption) ([]Entry, error) {
	span := s.tracer.StartSpan(ctx, "S3Storage.List")
	defer span.End()

	start := time.Now()
	defer func() {
		s.metrics.RecordDuration("storage.s3.list.duration", time.Since(start))
	}()

	// Parse the URI
	parsedURI, err := ParseURI(uri)
	if err != nil {
		return nil, NewInvalidURIError("List", uri, err)
	}

	// Apply options
	opts := &ListOptions{}
	applyListOptions(opts, options)

	// Build list objects input
	var maxKeys int32
	if opts.MaxKeys.IsSet() {
		maxKeys = int32(opts.MaxKeys.Get())
	} else {
		maxKeys = 1000 // Default max keys
	}

	input := &s3.ListObjectsV2Input{
		Bucket:  aws.String(parsedURI.Bucket),
		MaxKeys: aws.Int32(maxKeys),
	}

	// Set prefix if provided
	if opts.Prefix.IsSet() {
		// If URI already has a key path, append it to the prefix
		prefix := opts.Prefix.Get()
		if parsedURI.Key != "" {
			if !strings.HasSuffix(parsedURI.Key, "/") && !strings.HasPrefix(prefix, "/") {
				prefix = parsedURI.Key + "/" + prefix
			} else {
				prefix = parsedURI.Key + prefix
			}
		}
		input.Prefix = aws.String(prefix)
	} else if parsedURI.Key != "" {
		// Use the URI key as prefix if no prefix option
		input.Prefix = aws.String(parsedURI.Key)
	}

	// Set delimiter if provided and not in recursive mode
	if !opts.Recursive.Or(false) && opts.Delimiter.IsSet() {
		input.Delimiter = aws.String(opts.Delimiter.Get())
	} else if !opts.Recursive.Or(false) {
		// Default delimiter if not recursive
		input.Delimiter = aws.String("/")
	}

	// Set start-after if provided
	if opts.StartAfter.IsSet() {
		input.StartAfter = aws.String(opts.StartAfter.Get())
	}

	// List the objects
	var entries []Entry
	var continuationToken *string

	for {
		if continuationToken != nil {
			input.ContinuationToken = continuationToken
		}

		result, err := s.client.ListObjectsV2(ctx, input)
		if err != nil {
			return nil, TranslateAWSError("List", parsedURI.Bucket, err)
		}

		// Process common prefixes (directories)
		for _, prefix := range result.CommonPrefixes {
			if prefix.Prefix != nil {
				// Remove the input prefix to get relative key
				relativeKey := *prefix.Prefix
				if input.Prefix != nil && strings.HasPrefix(relativeKey, *input.Prefix) {
					relativeKey = strings.TrimPrefix(relativeKey, *input.Prefix)
				}

				entries = append(entries, Entry{
					Key:      relativeKey,
					IsPrefix: true,
					Metadata: Metadata{},
				})
			}
		}

		// Process objects
		for _, object := range result.Contents {
			if object.Key == nil {
				continue
			}

			// Remove the input prefix to get relative key
			relativeKey := *object.Key
			if input.Prefix != nil && strings.HasPrefix(relativeKey, *input.Prefix) {
				relativeKey = strings.TrimPrefix(relativeKey, *input.Prefix)
			}

			// Skip empty keys (happens with exact prefix match)
			if relativeKey == "" {
				continue
			}

			entry := Entry{
				Key:      relativeKey,
				IsPrefix: false,
				Metadata: Metadata{
					ETag:         strings.Trim(aws.ToString(object.ETag), "\""),
					LastModified: aws.ToTime(object.LastModified),
					Size:         aws.ToInt64(object.Size),
					StorageClass: string(object.StorageClass),
				},
			}

			// If metadata requested and not a prefix, get full metadata
			if opts.IncludeMetadata.Or(false) && !entry.IsPrefix {
				objKey := *object.Key
				headInput := &s3.HeadObjectInput{
					Bucket: aws.String(parsedURI.Bucket),
					Key:    aws.String(objKey),
				}

				headResult, headErr := s.client.HeadObject(ctx, headInput)
				if headErr == nil {
					entry.Metadata.ContentType = aws.ToString(headResult.ContentType)
					entry.Metadata.ContentEncoding = aws.ToString(headResult.ContentEncoding)
					entry.Metadata.UserMetadata = make(map[string]string)

					for k, v := range headResult.Metadata {
						entry.Metadata.UserMetadata[k] = v
					}
				}
			}

			entries = append(entries, entry)
		}

		// Check if there are more results
		if !aws.ToBool(result.IsTruncated) {
			break
		}

		continuationToken = result.NextContinuationToken
	}

	s.metrics.RecordCount("storage.s3.list.results", int64(len(entries)))

	return entries, nil
}

// S3StorageFactory creates an S3Storage from a URI
func S3StorageFactory(ctx context.Context, uri StorageURI) (Storage, error) {
	options := []S3StorageOption{}

	// Extract region from options
	if region := uri.Options.Get("region"); region != "" {
		options = append(options, WithS3Region(region))
	}

	// Extract endpoint from options
	if endpoint := uri.Options.Get("endpoint"); endpoint != "" {
		options = append(options, WithS3Endpoint(endpoint))
	}

	if profile := uri.Options.Get("profile"); profile != "" {
		options = append(options, WithS3Profile(profile))
	}

	// Extract credentials from options
	if accessKey := uri.Options.Get("access_key"); accessKey != "" {
		secretKey := uri.Options.Get("secret_key")
		options = append(options, WithS3Credentials(accessKey, secretKey))
	}

	// Extract session token from options
	if token := uri.Options.Get("session_token"); token != "" {
		options = append(options, WithS3SessionToken(token))
	}

	return NewS3Storage(options...)
}
