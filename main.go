package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cloud.google.com/go/pubsub"
	"cloud.google.com/go/storage"
)

type Event struct {
	Bucket string `json:"bucket"`
	Name   string `json:"name"`
}

func main() {
	var (
		topic           string
		cacheControl    string
		projectID       string
		shutdownTimeout time.Duration
	)
	flag.StringVar(&topic, "topic", "", "PubSub topic name to listen to bucket events")
	flag.StringVar(&cacheControl, "cache-control", "", "Cache-Control string to set")
	flag.StringVar(&projectID, "project-id", "", "project ID")
	flag.DurationVar(&shutdownTimeout, "shutdown-timeout", time.Minute, "shutdown timeout")
	flag.Parse()

	if topic == "" || cacheControl == "" || projectID == "" {
		log.Fatal("topic, cache-control, and project-id are required")
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	ctx := context.Background()

	cloudStorageClient, err := storage.NewClient(ctx)
	if err != nil {
		log.Fatal(err)
	}

	pubsubClient, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		log.Fatal(err)
	}
	defer pubsubClient.Close()

	ctx, cancel := context.WithCancel(ctx)

	slog.InfoContext(ctx, "starting worker", "topic", topic)

	if err := pubsubClient.Subscription(topic).Receive(ctx, func(ctx context.Context, m *pubsub.Message) {
		var e Event
		if err := json.Unmarshal(m.Data, &e); err != nil {
			slog.ErrorContext(ctx, "cannot decode message", "topic", topic, "error", err)
			m.Nack()
			return
		}

		attrs := storage.ObjectAttrsToUpdate{CacheControl: cacheControl}
		if _, err := cloudStorageClient.Bucket(e.Bucket).Object(e.Name).Update(ctx, attrs); err != nil {
			slog.ErrorContext(ctx, "cannot update object attributes", "topic", topic, "error", err, "bucket", e.Bucket, "name", e.Name)
			m.Nack()
			return
		}

		m.Ack()
	}); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("cannot consume message: topic=%s error=%s", topic, err)
	}

	// graceful shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		<-sig
		cancel()
	}()

	<-ctx.Done()

	slog.InfoContext(ctx, "worker stopped: ok", "topic", topic)
}
