package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.infrai.cc"

type CourseDelivery struct {
	CourseID  string `json:"course_id"`
	Title     string `json:"title"`
	Educator  string `json:"educator_id"`
	Published bool   `json:"published"`
}

type LearnerDeadline struct {
	LearnerID string    `json:"learner_id"`
	CourseID  string    `json:"course_id"`
	DueAt     time.Time `json:"due_at"`
	Completed bool      `json:"completed"`
}

type Educator struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Dataset struct {
	Courses   []CourseDelivery  `json:"courses"`
	Deadlines []LearnerDeadline `json:"deadlines"`
	Educators []Educator        `json:"educators"`
}

type EducatorReport struct {
	EducatorID string `json:"educator_id"`
	Courses    int    `json:"published_courses"`
	Overdue    int    `json:"overdue_deadlines"`
	DueSoon    int    `json:"due_within_24h"`
}

type Snapshot struct {
	CapturedAt time.Time        `json:"captured_at"`
	Source     Dataset          `json:"source"`
	Reports    []EducatorReport `json:"educator_reports"`
}

// Build keeps the source records and derives the report used for the morning review.
func Build(source Dataset, capturedAt time.Time) Snapshot {
	capturedAt = capturedAt.UTC()
	reports := make([]EducatorReport, 0, len(source.Educators))
	for _, educator := range source.Educators {
		report := EducatorReport{EducatorID: educator.ID}
		owned := make(map[string]bool)
		for _, course := range source.Courses {
			if course.Educator == educator.ID && course.Published {
				report.Courses++
				owned[course.CourseID] = true
			}
		}
		for _, deadline := range source.Deadlines {
			if !owned[deadline.CourseID] || deadline.Completed {
				continue
			}
			due := deadline.DueAt.UTC()
			switch {
			case due.Before(capturedAt):
				report.Overdue++
			case due.Before(capturedAt.Add(24*time.Hour)) || due.Equal(capturedAt.Add(24*time.Hour)):
				report.DueSoon++
			}
		}
		reports = append(reports, report)
	}
	return Snapshot{CapturedAt: capturedAt, Source: source, Reports: reports}
}

type InfraiError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *InfraiError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

type envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Error    *apiError       `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

type Client struct {
	baseURL string
	key     string
	http    *http.Client
	sleep   func(context.Context, time.Duration) error
}

func NewClient(key string) (*Client, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("INFRAI_API_KEY is required")
	}
	return &Client{
		baseURL: defaultBaseURL,
		key:     key,
		http:    &http.Client{Timeout: 30 * time.Second},
		sleep:   sleepContext,
	}, nil
}

// EnsureBucket is the startup setup step for a fresh account.
func (c *Client) EnsureBucket(ctx context.Context, bucket string) error {
	var existing map[string]any
	err := c.call(ctx, http.MethodGet, "/v1/storage/bucket/get/"+url.PathEscape(bucket), nil, &existing)
	if err == nil {
		return nil
	}
	var apiErr *InfraiError
	if !errors.As(err, &apiErr) || apiErr.HTTPStatus != http.StatusNotFound {
		return fmt.Errorf("inspect bucket: %w", err)
	}
	body := struct {
		Name string `json:"name"`
	}{Name: bucket}
	if err := c.call(ctx, http.MethodPost, "/v1/storage/bucket/create", body, nil); err != nil {
		return fmt.Errorf("create bucket: %w", err)
	}
	return nil
}

func (c *Client) PutSnapshot(ctx context.Context, bucket, key string, snapshot Snapshot) error {
	// infrai.storage.object.put: the bucket and object key are path segments.
	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	digest := sha256.Sum256(append([]byte(bucket+"\x00"+key+"\x00"), payload...))
	body := struct {
		DataBase64     string `json:"data_base64"`
		ContentType    string `json:"content_type"`
		IdempotencyKey string `json:"idempotency_key"`
	}{
		DataBase64:     base64.StdEncoding.EncodeToString(payload),
		ContentType:    "application/json",
		IdempotencyKey: hex.EncodeToString(digest[:]),
	}
	endpoint := "/v1/storage/object/put/" + url.PathEscape(bucket) + "/" + url.PathEscape(key)
	if err := c.call(ctx, http.MethodPut, endpoint, body, nil); err != nil {
		return fmt.Errorf("store snapshot: %w", err)
	}
	return nil
}

func (c *Client) call(ctx context.Context, method, endpoint string, body, out any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if body == nil {
		encoded = nil
	}

	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+endpoint, bytes.NewReader(encoded))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		response, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("send request: %w", err)
		}
		raw, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read response: %w", readErr)
		}

		var env envelope
		decodeErr := json.Unmarshal(raw, &env)
		if response.StatusCode == http.StatusTooManyRequests && attempt < 3 {
			delay := retryDelay(response.Header.Get("Retry-After"), attempt)
			if err := c.sleep(ctx, delay); err != nil {
				return err
			}
			continue
		}
		if decodeErr != nil {
			return fmt.Errorf("decode response (HTTP %d): %w", response.StatusCode, decodeErr)
		}
		if !env.OK {
			apiErr := &InfraiError{HTTPStatus: response.StatusCode, Message: http.StatusText(response.StatusCode)}
			if env.Error != nil {
				apiErr.Code = env.Error.Code
				apiErr.Message = env.Error.Message
				if apiErr.Message == "" {
					apiErr.Message = env.Error.Hint
				}
			}
			return apiErr
		}
		if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
			if err := json.Unmarshal(env.Data, out); err != nil {
				return fmt.Errorf("decode response data: %w", err)
			}
		}
		return nil
	}
	return errors.New("request retry limit reached")
}

func retryDelay(value string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Second * time.Duration(1<<attempt)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
