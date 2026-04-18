package config

import (
	"reflect"
	"testing"
)

func TestStatusBucketMatches(t *testing.T) {
	b := StatusBucket{Default: "In Progress", Aliases: []string{"WIP", "Dev"}}

	cases := map[string]bool{
		"In Progress":   true,
		"in progress":   true,
		"  IN PROGRESS": true,
		"wip":           true,
		"Dev":           true,
		"done":          false,
		"":              false,
		"   ":           false,
	}
	for in, want := range cases {
		if got := b.Matches(in); got != want {
			t.Errorf("Matches(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestStatusBucketMatchesEmptyDefault(t *testing.T) {
	b := StatusBucket{Aliases: []string{"Alias"}}
	if b.Matches("") {
		t.Error("empty name must not match")
	}
	if !b.Matches("alias") {
		t.Error("alias should match case-insensitively")
	}
}

func TestStatusBucketAll(t *testing.T) {
	b := StatusBucket{
		Default: "Done",
		Aliases: []string{"", "done", "Completed", "COMPLETED"},
	}
	got := b.All()
	want := []string{"Done", "Completed"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("All() = %v, want %v", got, want)
	}
}

func TestStatusBucketAllEmpty(t *testing.T) {
	b := StatusBucket{}
	if got := b.All(); len(got) != 0 {
		t.Errorf("empty bucket All() = %v, want empty", got)
	}
}
