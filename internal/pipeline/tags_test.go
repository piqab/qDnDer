package pipeline

import (
	"reflect"
	"testing"
	"time"
)

func TestExpandTags(t *testing.T) {
	c := TagContext{Tag: "v1.2.3", Branch: "feature/x", SHA: "0123456789abcdef0123", Now: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}
	got := ExpandTags([]string{"latest", "{tag}", "{branch}", "{sha:12}", "{date}", "{tag}-{date}"}, c)
	want := []string{"latest", "v1.2.3", "feature-x", "0123456789ab", "20260102", "v1.2.3-20260102"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestExpandTagsDropsEmpty(t *testing.T) {
	c := TagContext{Branch: "main", SHA: "abc"}
	got := ExpandTags([]string{"{tag}", "latest", "{sha:99}", "{bogus}", "latest"}, c)
	want := []string{"latest"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestExpandValue(t *testing.T) {
	c := TagContext{Tag: "v2.0.0"}
	if v := ExpandValue("{tag}", c); v != "v2.0.0" {
		t.Fatalf("got %q", v)
	}
	if v := ExpandValue("{tag}", TagContext{}); v != "" {
		t.Fatalf("empty tag must resolve to empty, got %q", v)
	}
	if v := ExpandValue("plain", c); v != "plain" {
		t.Fatalf("got %q", v)
	}
}
