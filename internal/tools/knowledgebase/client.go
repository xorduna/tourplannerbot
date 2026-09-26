// Package knowledgebase implements native tools backed by an S3-compatible
// object store containing Diana Barcelona's structured website content.
package knowledgebase

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const maximumObjectBytes = 2 << 20

// Config contains the S3-compatible credentials and location used by the
// knowledge-base client.
type Config struct {
	Endpoint    string
	AccessKey   string
	SecretKey   string
	BucketName  string
	Region      string
	CallTimeout time.Duration
}

// Object describes one object returned by the bucket inventory.
type Object struct {
	Key          string
	ETag         string
	LastModified time.Time
}

// Client lists and retrieves objects through the S3 REST API, authenticating
// each request with AWS Signature Version 4 as supported by Spaces.
type Client struct {
	endpoint   *url.URL
	accessKey  string
	secretKey  string
	bucketName string
	region     string
	httpClient *http.Client
	now        func() time.Time
}

// NewClient validates configuration and constructs a reusable knowledge-base
// client. It does not contact the object store during startup.
func NewClient(configuration Config) (*Client, error) {
	parsedEndpoint, err := url.ParseRequestURI(strings.TrimSpace(configuration.Endpoint))
	if err != nil || parsedEndpoint.Host == "" || (parsedEndpoint.Scheme != "http" && parsedEndpoint.Scheme != "https") {
		return nil, fmt.Errorf("knowledge-base endpoint must be an absolute HTTP or HTTPS URL")
	}
	if strings.TrimSpace(configuration.AccessKey) == "" {
		return nil, fmt.Errorf("knowledge-base access key is required")
	}
	if strings.TrimSpace(configuration.SecretKey) == "" {
		return nil, fmt.Errorf("knowledge-base secret key is required")
	}
	if strings.TrimSpace(configuration.BucketName) == "" {
		return nil, fmt.Errorf("knowledge-base bucket name is required")
	}
	if strings.TrimSpace(configuration.Region) == "" {
		return nil, fmt.Errorf("knowledge-base region is required")
	}
	if configuration.CallTimeout <= 0 {
		return nil, fmt.Errorf("knowledge-base call timeout must be positive")
	}

	return &Client{
		endpoint:   parsedEndpoint,
		accessKey:  strings.TrimSpace(configuration.AccessKey),
		secretKey:  strings.TrimSpace(configuration.SecretKey),
		bucketName: strings.TrimSpace(configuration.BucketName),
		region:     strings.TrimSpace(configuration.Region),
		httpClient: &http.Client{Timeout: configuration.CallTimeout},
		now:        time.Now,
	}, nil
}

// ListObjects returns every object directly below the requested prefix. S3
// pagination stays inside the client so callers get one deterministic slice.
func (client *Client) ListObjects(applicationContext context.Context, prefix string) ([]Object, error) {
	objects := make([]Object, 0)
	continuationToken := ""
	for {
		queryValues := url.Values{"list-type": []string{"2"}, "prefix": []string{prefix}}
		if continuationToken != "" {
			queryValues.Set("continuation-token", continuationToken)
		}
		responseBody, err := client.call(applicationContext, http.MethodGet, "", queryValues)
		if err != nil {
			return nil, err
		}
		listResponse := struct {
			Contents []struct {
				Key          string `xml:"Key"`
				ETag         string `xml:"ETag"`
				LastModified string `xml:"LastModified"`
			} `xml:"Contents"`
			IsTruncated           bool   `xml:"IsTruncated"`
			NextContinuationToken string `xml:"NextContinuationToken"`
		}{}
		if err := xml.Unmarshal(responseBody, &listResponse); err != nil {
			return nil, fmt.Errorf("decode knowledge-base object list: %w", err)
		}
		for _, listedObject := range listResponse.Contents {
			lastModified, err := time.Parse(time.RFC3339, strings.TrimSpace(listedObject.LastModified))
			if err != nil && strings.TrimSpace(listedObject.LastModified) != "" {
				return nil, fmt.Errorf("decode knowledge-base object modification time for %q: %w", listedObject.Key, err)
			}
			objects = append(objects, Object{Key: listedObject.Key, ETag: strings.Trim(listedObject.ETag, "\""), LastModified: lastModified})
		}
		if !listResponse.IsTruncated {
			break
		}
		continuationToken = strings.TrimSpace(listResponse.NextContinuationToken)
		if continuationToken == "" {
			return nil, fmt.Errorf("knowledge-base object list is truncated without a continuation token")
		}
	}
	sort.Slice(objects, func(firstIndex int, secondIndex int) bool { return objects[firstIndex].Key < objects[secondIndex].Key })
	return objects, nil
}

// GetObject retrieves one object body after the caller has selected a key from
// the trusted bucket inventory.
func (client *Client) GetObject(applicationContext context.Context, key string) ([]byte, error) {
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("knowledge-base object key is required")
	}
	return client.call(applicationContext, http.MethodGet, key, nil)
}

// call signs and executes one S3 request, returning only successful bounded
// response bodies. Credentials and upstream response bodies never enter errors.
func (client *Client) call(applicationContext context.Context, method string, key string, queryValues url.Values) ([]byte, error) {
	requestURL := *client.endpoint
	requestURL.Host = client.bucketName + "." + client.endpoint.Host
	requestURL.Path = strings.TrimRight(client.endpoint.Path, "/") + "/" + strings.TrimLeft(key, "/")
	requestURL.RawQuery = queryValues.Encode()
	request, err := http.NewRequestWithContext(applicationContext, method, requestURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create knowledge-base request: %w", err)
	}
	client.sign(request)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call knowledge-base object store: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := readObjectResponse(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read knowledge-base object store response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("knowledge-base object store returned HTTP %d", response.StatusCode)
	}
	return responseBody, nil
}

// sign adds the AWS Signature Version 4 headers required by DigitalOcean
// Spaces for the request's method, path, query, and empty payload.
func (client *Client) sign(request *http.Request) {
	requestTime := client.now().UTC()
	requestDate := requestTime.Format("20060102")
	requestTimestamp := requestTime.Format("20060102T150405Z")
	payloadHash := sha256Hex("")
	canonicalURI := request.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalQuery := canonicalQueryString(request.URL.Query())
	canonicalHeaders := "host:" + request.URL.Host + "\n" + "x-amz-content-sha256:" + payloadHash + "\n" + "x-amz-date:" + requestTimestamp + "\n"
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalRequest := strings.Join([]string{request.Method, canonicalURI, canonicalQuery, canonicalHeaders, signedHeaders, payloadHash}, "\n")
	credentialScope := requestDate + "/" + client.region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{"AWS4-HMAC-SHA256", requestTimestamp, credentialScope, sha256Hex(canonicalRequest)}, "\n")
	signingKey := hmacSHA256(hmacSHA256(hmacSHA256(hmacSHA256([]byte("AWS4"+client.secretKey), requestDate), client.region), "s3"), "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	request.Header.Set("X-Amz-Content-Sha256", payloadHash)
	request.Header.Set("X-Amz-Date", requestTimestamp)
	request.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+client.accessKey+"/"+credentialScope+", SignedHeaders="+signedHeaders+", Signature="+signature)
}

// canonicalQueryString produces the percent-encoded, sorted query format
// mandated by AWS Signature Version 4 instead of Go's plus-for-space encoding.
func canonicalQueryString(queryValues url.Values) string {
	keys := make([]string, 0, len(queryValues))
	for key := range queryValues {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	encodedPairs := make([]string, 0)
	for _, key := range keys {
		values := append([]string(nil), queryValues[key]...)
		sort.Strings(values)
		for _, value := range values {
			encodedPairs = append(encodedPairs, awsPercentEncode(key)+"="+awsPercentEncode(value))
		}
	}
	return strings.Join(encodedPairs, "&")
}

// awsPercentEncode escapes a query component according to RFC 3986, which is
// the encoding required by the S3 signing algorithm.
func awsPercentEncode(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

// sha256Hex returns the lowercase SHA-256 digest of one string.
func sha256Hex(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// hmacSHA256 calculates one link in the Signature Version 4 signing chain.
func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

// readObjectResponse limits one KB object so an accidentally large object
// cannot consume unbounded application memory.
func readObjectResponse(responseBody io.Reader) ([]byte, error) {
	limitedReader := io.LimitReader(responseBody, maximumObjectBytes+1)
	responseBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, err
	}
	if len(responseBytes) > maximumObjectBytes {
		return nil, fmt.Errorf("response exceeded %d bytes", maximumObjectBytes)
	}
	return responseBytes, nil
}
