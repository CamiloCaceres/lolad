package fake_test

import (
	"context"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/web/fake"
)

func TestSeedTrafficIsNewestFirstAcrossPages(t *testing.T) {
	s := fake.New(fake.Options{})
	defer s.Close()
	first, err := s.Traffic(context.Background(), 60, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 60 {
		t.Fatalf("want a full audit page, got %d", len(first))
	}
	for i := 1; i < len(first); i++ {
		if first[i].CreatedAt.After(first[i-1].CreatedAt) {
			t.Fatal("seed violates Traffic's newest-first contract")
		}
	}
	older, err := s.Traffic(context.Background(), 60, first[len(first)-1].CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(older) == 0 {
		t.Fatal("seed does not exercise pagination")
	}
	for _, m := range older {
		if !m.CreatedAt.Before(first[len(first)-1].CreatedAt) {
			t.Fatal("older cursor repeated a message")
		}
	}
}
