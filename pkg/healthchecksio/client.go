// Package healthchecksio provides a simple, retryable HTTP client for healthchecks.io (v3 API only)
package healthchecksio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/moov-io/base/telemetry"

	"github.com/hashicorp/go-retryablehttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type Client interface {
	// CreateCheck creates a new check
	CreateCheck(ctx context.Context, check *CreateCheck) (*Check, error)

	// GetChecks lists all checks (supports query params: slug, tags)
	GetChecks(ctx context.Context, req GetChecks) (*CheckListResponse, error)

	// GetCheck retrieves a single check by UUID or unique_key
	GetCheck(ctx context.Context, identifier string) (*Check, error)

	// UpdateCheck updates an existing check by UUID
	UpdateCheck(ctx context.Context, uuid string, updates *UpdateCheck) (*Check, error)

	// DeleteCheck deletes a check by UUID
	DeleteCheck(ctx context.Context, uuid string) (*Check, error)

	// PauseCheck pauses a check by UUID
	PauseCheck(ctx context.Context, uuid string) (*Check, error)

	// ResumeCheck resumes a paused check by UUID
	ResumeCheck(ctx context.Context, uuid string) (*Check, error)

	// GetPings lists pings for a check by UUID or unique_key
	GetPings(ctx context.Context, identifier string) (*PingListResponse, error)

	// GetPingBody retrieves the body of a specific ping by UUID, ping number (n), and unique_key if needed
	GetPingBody(ctx context.Context, uuid string, n int) (string, error)

	// GetFlips lists status flips for a check by UUID or unique_key (supports query params: seconds, start, end)
	GetFlips(ctx context.Context, identifier string, params GetFlipsRequest) (*FlipListResponse, error)

	// Ping sends a ping to a check (success by default; supports hc-ping.com UUID or /api/v3/ping/<unique_key>)
	Ping(ctx context.Context, checkURL string, body string, opts ...PingOption) error
}

// client is a Healthchecks.io v3 API client
type client struct {
	apiKey     string
	baseURL    string // https://healthchecks.io/api/v3
	httpClient *retryablehttp.Client
}

var _ Client = (&client{})

// NewClient creates a new Healthchecks.io v3 client
// apiKey: your API key (read-write or read-only)
func NewClient(apiKey string) Client {
	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 5
	retryClient.RetryWaitMin = 500 * time.Millisecond
	retryClient.RetryWaitMax = 4 * time.Second
	retryClient.Logger = nil // silence logs in production

	return &client{
		apiKey:     apiKey,
		baseURL:    "https://healthchecks.io/api/v3",
		httpClient: retryClient,
	}
}

func (c *client) buildAddress(slugs ...string) (*url.URL, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("problem parsing baseAddress: %w", err)
	}
	return base.JoinPath(slugs...), nil
}

type Error struct {
	Err string `json:"error"`
}

func (e Error) Error() string {
	return e.Err
}

type CreateCheck struct {
	Name              string   `json:"name,omitempty"`
	Slug              string   `json:"slug,omitempty"`
	Tags              string   `json:"tags,omitempty"`
	Description       string   `json:"desc,omitempty"`
	Timeout           int      `json:"timeout,omitempty"`
	Grace             int      `json:"grace,omitempty"`
	Schedule          string   `json:"schedule,omitempty"`
	Timezone          string   `json:"tz,omitempty"`
	ManualResume      bool     `json:"manual_resume,omitempty"`
	Methods           string   `json:"methods,omitempty"`
	Channels          string   `json:"channels,omitempty"`
	Unique            []string `json:"unique,omitempty"`
	StartKeywords     string   `json:"start_kw,omitempty"`
	SuccessKeywords   string   `json:"success_kw,omitempty"`
	FailureKeywords   string   `json:"failure_kw,omitempty"`
	FilterSubject     bool     `json:"filter_subject,omitempty"`
	FilterBody        bool     `json:"filter_body,omitempty"`
	FilterHttpBody    bool     `json:"filter_http_body,omitempty"`
	FilterDefaultFail bool     `json:"filter_default_fail,omitempty"`
}

type UpdateCheck struct {
	Name              string `json:"name,omitempty"`
	Slug              string `json:"slug,omitempty"`
	Tags              string `json:"tags,omitempty"`
	Description       string `json:"desc,omitempty"`
	Timeout           int    `json:"timeout,omitempty"`
	Grace             int    `json:"grace,omitempty"`
	Schedule          string `json:"schedule,omitempty"`
	Timezone          string `json:"tz,omitempty"`
	ManualResume      bool   `json:"manual_resume,omitempty"`
	Methods           string `json:"methods,omitempty"`
	Channels          string `json:"channels,omitempty"`
	StartKeywords     string `json:"start_kw,omitempty"`
	SuccessKeywords   string `json:"success_kw,omitempty"`
	FailureKeywords   string `json:"failure_kw,omitempty"`
	FilterSubject     bool   `json:"filter_subject,omitempty"`
	FilterBody        bool   `json:"filter_body,omitempty"`
	FilterHttpBody    bool   `json:"filter_http_body,omitempty"`
	FilterDefaultFail bool   `json:"filter_default_fail,omitempty"`
}

// Check represents a Healthchecks.io check (from API responses)
type Check struct {
	Name              string `json:"name"`
	Slug              string `json:"slug"`
	Tags              string `json:"tags"`
	Desc              string `json:"desc"`
	Grace             int    `json:"grace"`
	NPings            int    `json:"n_pings"`
	Status            string `json:"status"`
	Started           bool   `json:"started"`
	LastPing          any    `json:"last_ping"`
	NextPing          any    `json:"next_ping"`
	ManualResume      bool   `json:"manual_resume"`
	Methods           string `json:"methods"`
	Subject           string `json:"subject"`
	SubjectFail       string `json:"subject_fail"`
	StartKw           string `json:"start_kw"`
	SuccessKw         string `json:"success_kw"`
	FailureKw         string `json:"failure_kw"`
	FilterSubject     bool   `json:"filter_subject"`
	FilterBody        bool   `json:"filter_body"`
	FilterHTTPBody    bool   `json:"filter_http_body"`
	FilterDefaultFail bool   `json:"filter_default_fail"`
	BadgeURL          string `json:"badge_url"`
	UUID              string `json:"uuid"`
	PingURL           string `json:"ping_url"`
	UpdateURL         string `json:"update_url"`
	PauseURL          string `json:"pause_url"`
	ResumeURL         string `json:"resume_url"`
	Channels          string `json:"channels"`
	Timeout           int    `json:"timeout"`
}

// CheckListResponse wraps the list of checks
type CheckListResponse struct {
	Checks []Check `json:"checks"`
}

// Ping represents a ping (from API responses)
type Ping struct {
	Type       string    `json:"type"`
	Date       time.Time `json:"date"`
	N          int       `json:"n"`
	Scheme     string    `json:"scheme"`
	RemoteAddr string    `json:"remote_addr"`
	Method     string    `json:"method"`
	Ua         string    `json:"ua"`
	Rid        string    `json:"rid"`
	Duration   float64   `json:"duration,omitempty"`
	BodyURL    *string   `json:"body_url"`
}

// PingListResponse wraps the list of pings
type PingListResponse struct {
	Pings []Ping `json:"pings"`
}

// Flip represents a status flip (from API responses)
type Flip struct {
	Timestamp string `json:"timestamp"`
	Up        int    `json:"up"`
}

// FlipListResponse is a direct array of flips
type FlipListResponse struct {
	Flips []Flip `json:"flips"`
}

// CreateCheck creates a new check
func (c *client) CreateCheck(ctx context.Context, check *CreateCheck) (*Check, error) {
	ctx, span := telemetry.StartSpan(ctx, "healthchecksio-api-create-check", trace.WithAttributes(
		attribute.String("check.name", check.Name),
		attribute.String("check.slug", check.Slug),
	))
	defer span.End()

	reqBody, err := json.Marshal(check)
	if err != nil {
		return nil, err
	}

	address, err := c.buildAddress("/checks/")
	if err != nil {
		return nil, fmt.Errorf("get checks: %v", err)
	}

	req, err := retryablehttp.NewRequestWithContext(ctx, "POST", address.String(), bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		var err2 Error
		json.NewDecoder(resp.Body).Decode(&err2)
		return nil, fmt.Errorf("create check failed with %d: %v", resp.StatusCode, err2)
	}

	var created Check
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return nil, err
	}
	return &created, nil
}

type GetChecks struct {
	Slug string
	Tags string
}

// GetChecks lists all checks (supports query params: slug, tags)
func (c *client) GetChecks(ctx context.Context, params GetChecks) (*CheckListResponse, error) {
	ctx, span := telemetry.StartSpan(ctx, "healthchecksio-api-get-checks", trace.WithAttributes(
		attribute.String("check.slug", params.Slug),
		attribute.String("check.tags", params.Tags),
	))
	defer span.End()

	address, err := c.buildAddress("/checks/")
	if err != nil {
		return nil, fmt.Errorf("get checks: %v", err)
	}

	q := make(url.Values)
	if params.Slug != "" {
		q.Set("slug", params.Slug)
	}
	if params.Tags != "" {
		q.Set("tags", params.Tags)
	}
	address.RawQuery = q.Encode()

	req, err := retryablehttp.NewRequestWithContext(ctx, "GET", address.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var err2 Error
		json.NewDecoder(resp.Body).Decode(&err2)
		return nil, fmt.Errorf("get checks failed with %d: %v", resp.StatusCode, err2)
	}

	var list CheckListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return &list, nil
}

// GetCheck retrieves a single check by UUID or unique_key
func (c *client) GetCheck(ctx context.Context, identifier string) (*Check, error) {
	ctx, span := telemetry.StartSpan(ctx, "healthchecksio-api-get-check", trace.WithAttributes(
		attribute.String("check.identifier", identifier),
	))
	defer span.End()

	address, err := c.buildAddress("/checks/", identifier)
	if err != nil {
		return nil, fmt.Errorf("get checks: %v", err)
	}

	req, err := retryablehttp.NewRequestWithContext(ctx, "GET", address.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var err2 Error
		json.NewDecoder(resp.Body).Decode(&err2)
		return nil, fmt.Errorf("get check failed with %d: %v", resp.StatusCode, err2)
	}

	var ch Check
	if err := json.NewDecoder(resp.Body).Decode(&ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// UpdateCheck updates an existing check by UUID
func (c *client) UpdateCheck(ctx context.Context, uuid string, update *UpdateCheck) (*Check, error) {
	ctx, span := telemetry.StartSpan(ctx, "healthchecksio-api-update-check", trace.WithAttributes(
		attribute.String("check.uuid", uuid),
		attribute.String("check.name", update.Name),
		attribute.String("check.slug", update.Slug),
	))
	defer span.End()

	reqBody, err := json.Marshal(update)
	if err != nil {
		return nil, err
	}

	address, err := c.buildAddress("/checks/", uuid)
	if err != nil {
		return nil, fmt.Errorf("get checks: %v", err)
	}

	req, err := retryablehttp.NewRequestWithContext(ctx, "POST", address.String(), bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var err2 Error
		json.NewDecoder(resp.Body).Decode(&err2)
		return nil, fmt.Errorf("update check failed with %d: %v", resp.StatusCode, err2)
	}

	var updated Check
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

// DeleteCheck deletes a check by UUID
func (c *client) DeleteCheck(ctx context.Context, uuid string) (*Check, error) {
	ctx, span := telemetry.StartSpan(ctx, "healthchecksio-api-delete-check", trace.WithAttributes(
		attribute.String("check.uuid", uuid),
	))
	defer span.End()

	address, err := c.buildAddress("/checks/", uuid)
	if err != nil {
		return nil, fmt.Errorf("get checks: %v", err)
	}

	req, err := retryablehttp.NewRequestWithContext(ctx, "DELETE", address.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var err2 Error
		json.NewDecoder(resp.Body).Decode(&err2)
		return nil, fmt.Errorf("delete check failed with %d: %v", resp.StatusCode, err2)
	}

	var deleted Check
	if err := json.NewDecoder(resp.Body).Decode(&deleted); err != nil {
		return nil, err
	}
	return &deleted, nil
}

// PauseCheck pauses a check by UUID
func (c *client) PauseCheck(ctx context.Context, uuid string) (*Check, error) {
	ctx, span := telemetry.StartSpan(ctx, "healthchecksio-api-pause-check", trace.WithAttributes(
		attribute.String("check.uuid", uuid),
	))
	defer span.End()

	address, err := c.buildAddress("/checks/", uuid, "/pause")
	if err != nil {
		return nil, fmt.Errorf("get checks: %v", err)
	}

	req, err := retryablehttp.NewRequestWithContext(ctx, "POST", address.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var err2 Error
		json.NewDecoder(resp.Body).Decode(&err2)
		return nil, fmt.Errorf("pause check failed with %d: %v", resp.StatusCode, err2)
	}

	var paused Check
	if err := json.NewDecoder(resp.Body).Decode(&paused); err != nil {
		return nil, err
	}
	return &paused, nil
}

// ResumeCheck resumes a paused check by UUID
func (c *client) ResumeCheck(ctx context.Context, uuid string) (*Check, error) {
	ctx, span := telemetry.StartSpan(ctx, "healthchecksio-api-resume-check", trace.WithAttributes(
		attribute.String("check.uuid", uuid),
	))
	defer span.End()

	address, err := c.buildAddress("/checks/", uuid, "/resume")
	if err != nil {
		return nil, fmt.Errorf("get checks: %v", err)
	}

	req, err := retryablehttp.NewRequestWithContext(ctx, "POST", address.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var err2 Error
		json.NewDecoder(resp.Body).Decode(&err2)
		return nil, fmt.Errorf("resume check failed with %d: %v", resp.StatusCode, err2)
	}

	var resumed Check
	if err := json.NewDecoder(resp.Body).Decode(&resumed); err != nil {
		return nil, err
	}
	return &resumed, nil
}

// GetPings lists pings for a check by UUID or unique_key
func (c *client) GetPings(ctx context.Context, identifier string) (*PingListResponse, error) {
	ctx, span := telemetry.StartSpan(ctx, "healthchecksio-api-get-pings", trace.WithAttributes(
		attribute.String("check.identifier", identifier),
	))
	defer span.End()

	address, err := c.buildAddress("/checks/", identifier, "/pings/")
	if err != nil {
		return nil, fmt.Errorf("get checks: %v", err)
	}

	req, err := retryablehttp.NewRequestWithContext(ctx, "GET", address.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var err2 Error
		json.NewDecoder(resp.Body).Decode(&err2)
		return nil, fmt.Errorf("get check failed with %d: %v", resp.StatusCode, err2)
	}

	var list PingListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return &list, nil
}

// GetPingBody retrieves the body of a specific ping by UUID, ping number (n), and unique_key if needed
func (c *client) GetPingBody(ctx context.Context, uuid string, n int) (string, error) {
	ctx, span := telemetry.StartSpan(ctx, "healthchecksio-api-get-ping-body", trace.WithAttributes(
		attribute.String("check.uuid", uuid),
	))
	defer span.End()

	address, err := c.buildAddress("/checks/", uuid, "/pings/", strconv.Itoa(n), "/body")
	if err != nil {
		return "", fmt.Errorf("get checks: %v", err)
	}

	req, err := retryablehttp.NewRequestWithContext(ctx, "GET", address.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Api-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var err2 Error
		json.NewDecoder(resp.Body).Decode(&err2)
		return "", fmt.Errorf("get ping body failed with %d: %v", resp.StatusCode, err2)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

type GetFlipsRequest struct {
	Seconds int
	Start   int64
	End     int64
}

// GetFlips lists status flips for a check by UUID or unique_key (supports query params: seconds, start, end)
func (c *client) GetFlips(ctx context.Context, identifier string, params GetFlipsRequest) (*FlipListResponse, error) {
	ctx, span := telemetry.StartSpan(ctx, "healthchecksio-api-get-flips", trace.WithAttributes(
		attribute.String("check.identifier", identifier),
	))
	defer span.End()

	address, err := c.buildAddress("/checks/", identifier, "/flips/")
	if err != nil {
		return nil, fmt.Errorf("get checks: %v", err)
	}

	q := make(url.Values)
	if params.Seconds > 0 {
		q.Set("seconds", strconv.Itoa(params.Seconds))
	}
	if params.Start > 0 {
		q.Set("start", fmt.Sprintf("%d", params.Start))
	}
	if params.End > 0 {
		q.Set("end", fmt.Sprintf("%d", params.End))
	}
	address.RawQuery = q.Encode()

	req, err := retryablehttp.NewRequestWithContext(ctx, "GET", address.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var err2 Error
		json.NewDecoder(resp.Body).Decode(&err2)
		return nil, fmt.Errorf("get flips failed with %d: %v", resp.StatusCode, err2)
	}

	var list FlipListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return &list, nil
}

// Ping sends a ping to a check (success by default; supports hc-ping.com UUID or /api/v3/ping/<unique_key>)
func (c *client) Ping(ctx context.Context, pingURL, body string, opts ...PingOption) error {
	ctx, span := telemetry.StartSpan(ctx, "healthchecksio-api-ping", trace.WithAttributes(
		attribute.String("check.ping_body", body),
		attribute.String("check.ping_url", pingURL),
	))
	defer span.End()

	addr, err := url.Parse(pingURL)
	if err != nil {
		return fmt.Errorf("parsing ping url: %v", err)
	}
	for i := range opts {
		addr = opts[i](addr)
	}

	req, err := retryablehttp.NewRequestWithContext(ctx, "POST", addr.String(), strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "go-healthchecks-client")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ping: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		bs, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ping failed with %d: %v", resp.StatusCode, string(bs))
	}

	return nil
}

// PingOption configures ping behavior
type PingOption func(*url.URL) *url.URL

// WithStart sends a failure ping
func WithStart() PingOption {
	return func(u *url.URL) *url.URL {
		return u.JoinPath("/start")
	}
}

func WithFail() PingOption {
	return func(u *url.URL) *url.URL {
		return u.JoinPath("/fail")
	}
}
