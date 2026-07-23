package knowledge

import (
	"testing"
	"testing/fstest"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"knowledge/database/execution-plan.md": &fstest.MapFile{Data: []byte(`---
id: execution-plan
title: Execution Plan
tags: [database, performance]
---

# Execution Plan

Body content here.
`)},
		"knowledge/database/no-front-matter.md": &fstest.MapFile{Data: []byte("# Just a title\n\nSome body.\n")},
	}
}

func TestListSortedAndParsesFrontMatter(t *testing.T) {
	docs, err := List(testFS(), "knowledge")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("List() returned %d docs, want 2", len(docs))
	}
	// sorted by ID: "execution-plan" < "no-front-matter"
	if docs[0].ID != "execution-plan" || docs[0].Title != "Execution Plan" {
		t.Errorf("unexpected doc[0] = %+v", docs[0])
	}
	if docs[1].ID != "no-front-matter" {
		t.Errorf("expected fallback ID from filename, got %+v", docs[1])
	}
}

func TestLookupCaseInsensitive(t *testing.T) {
	doc, err := Lookup(testFS(), "knowledge", "Execution-Plan")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if doc.ID != "execution-plan" {
		t.Errorf("Lookup() = %+v, want id execution-plan", doc)
	}
}

func TestLookupNotFound(t *testing.T) {
	if _, err := Lookup(testFS(), "knowledge", "does-not-exist"); err == nil {
		t.Error("expected error for unknown topic, got nil")
	}
}
