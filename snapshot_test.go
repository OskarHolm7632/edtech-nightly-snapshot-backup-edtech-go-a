package snapshot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBuildEducatorReport(t *testing.T) {
	now := time.Date(2026, time.September, 27, 2, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		data Dataset
		want EducatorReport
	}{
		{
			name: "counts open deadlines for published courses",
			data: Dataset{
				Educators: []Educator{{ID: "teacher-7", Name: "Ada"}},
				Courses: []CourseDelivery{
					{CourseID: "go-101", Educator: "teacher-7", Published: true},
					{CourseID: "draft-1", Educator: "teacher-7", Published: false},
				},
				Deadlines: []LearnerDeadline{
					{LearnerID: "l-1", CourseID: "go-101", DueAt: now.Add(-time.Hour)},
					{LearnerID: "l-2", CourseID: "go-101", DueAt: now.Add(8 * time.Hour)},
					{LearnerID: "l-3", CourseID: "go-101", DueAt: now.Add(2 * time.Hour), Completed: true},
					{LearnerID: "l-4", CourseID: "draft-1", DueAt: now.Add(-time.Hour)},
				},
			},
			want: EducatorReport{EducatorID: "teacher-7", Courses: 1, Overdue: 1, DueSoon: 1},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Build(test.data, now).Reports[0]
			if got != test.want {
				t.Fatalf("report = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestPutSnapshotRetries429WithSameIdempotencyKey(t *testing.T) {
	var calls int
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPut {
			t.Fatalf("method = %s", r.Method)
		}
		var body struct {
			IdempotencyKey string `json:"idempotency_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, body.IdempotencyKey)
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"ok":false,"error":{"code":"capacity","message":"retry later"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"data":{"key":"snapshots/2026-09-27.json"},"metadata":{}}`)
	}))
	defer server.Close()

	client, err := NewClient("test-key")
	if err != nil {
		t.Fatal(err)
	}
	client.baseURL = server.URL
	client.http = server.Client()
	client.sleep = func(context.Context, time.Duration) error { return nil }
	if err := client.PutSnapshot(context.Background(), "nightly", "snapshots/2026-09-27.json", Snapshot{}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("calls = %d, idempotency keys = %v", calls, keys)
	}
}
