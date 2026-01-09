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

	"cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/storage"
)

func main() {
	var projectID, topic, cacheControl string
	flag.StringVar(&projectID, "project_id", os.Getenv("PROJECT_ID"), "GCP Project ID")
	flag.StringVar(&topic, "topic", "", "PubSub topic name to listen for Cloud Storage events")
	flag.StringVar(&cacheControl, "cache-control", "", "Cache-Control string to set")
	flag.Parse()

	if topic == "" || cacheControl == "" || projectID == "" {
		log.Fatal("topic, cache-control, and project are required")
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	ctx := context.Background()

	cloudstorageClient, err := storage.NewClient(ctx)
	if err != nil {
		log.Fatal(err)
	}

	pubsubClient, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithCancel(ctx)

	slog.InfoContext(ctx, "starting worker", "topic", topic)

	if err := pubsubClient.Subscriber(topic).Receive(ctx, func(ctx context.Context, m *pubsub.Message) {
		var e struct {
			Bucket string `json:"bucket"`
			Name   string `json:"name"`
		}
		if err := json.Unmarshal(m.Data, &e); err != nil {
			slog.ErrorContext(ctx, "cannot decode message", "topic", topic, "error", err)
			m.Nack()
			return
		}

		attrs := storage.ObjectAttrsToUpdate{CacheControl: cacheControl}
		if _, err := cloudstorageClient.Bucket(e.Bucket).Object(e.Name).Update(ctx, attrs); err != nil {
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
