package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	snapshot "example.com/edtech-nightly-snapshot"
)

func main() {
	var input string
	var bucket string
	var hour int
	var once bool
	flag.StringVar(&input, "input", "sample-data.json", "course delivery JSON file")
	flag.StringVar(&bucket, "bucket", "edtech-nightly", "snapshot bucket")
	flag.IntVar(&hour, "hour", 2, "nightly UTC hour")
	flag.BoolVar(&once, "once", false, "run one snapshot and exit")
	flag.Parse()

	if hour < 0 || hour > 23 {
		log.Fatal("-hour must be between 0 and 23")
	}
	client, err := snapshot.NewClient(os.Getenv("INFRAI_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	run := func(at time.Time) error {
		raw, err := os.ReadFile(input)
		if err != nil {
			return fmt.Errorf("read input: %w", err)
		}
		var source snapshot.Dataset
		if err := json.Unmarshal(raw, &source); err != nil {
			return fmt.Errorf("decode input: %w", err)
		}
		if err := client.EnsureBucket(ctx, bucket); err != nil {
			return err
		}
		built := snapshot.Build(source, at)
		key := filepath.ToSlash(filepath.Join("snapshots", at.UTC().Format("2006-01-02")+".json"))
		if err := client.PutSnapshot(ctx, bucket, key, built); err != nil {
			return err
		}
		log.Printf("stored bucket=%s key=%s reports=%d", bucket, key, len(built.Reports))
		return nil
	}

	if once {
		if err := run(time.Now().UTC()); err != nil {
			log.Fatal(err)
		}
		return
	}
	for {
		now := time.Now().UTC()
		next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC)
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		log.Printf("next snapshot=%s", next.Format(time.RFC3339))
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			if !errors.Is(ctx.Err(), context.Canceled) {
				log.Print(ctx.Err())
			}
			return
		case at := <-timer.C:
			if err := run(at.UTC()); err != nil {
				log.Printf("snapshot failed: %v", err)
			}
		}
	}
}
