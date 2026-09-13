package rag

import (
	"reflect"
	"regexp"
	"slices"
	"testing"
)

func TestTermsToSparseVectorIsStableAndSorted(t *testing.T) {
	t.Parallel()

	terms := map[string]int{"dense": 2, "sparse": 1, "hybrid": 3}
	firstIndices, firstValues := termsToSparseVector(terms)
	secondIndices, secondValues := termsToSparseVector(terms)

	if !slices.IsSorted(firstIndices) {
		t.Fatalf("indices are not sorted: %v", firstIndices)
	}
	if !reflect.DeepEqual(firstIndices, secondIndices) || !reflect.DeepEqual(firstValues, secondValues) {
		t.Fatalf("sparse vector is not deterministic: (%v, %v) != (%v, %v)", firstIndices, firstValues, secondIndices, secondValues)
	}
}

func TestDeterministicUUID(t *testing.T) {
	t.Parallel()

	first := deterministicUUID("document#0")
	second := deterministicUUID("document#0")
	if first != second {
		t.Fatalf("UUID is not deterministic: %q != %q", first, second)
	}
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !pattern.MatchString(first) {
		t.Fatalf("UUID is not a valid version 5 UUID: %q", first)
	}
}

func TestBuildTSQuery(t *testing.T) {
	t.Parallel()

	if got, want := buildTSQuery([]string{"sparse", "dense", "dense"}), "dense | sparse"; got != want {
		t.Fatalf("buildTSQuery() = %q, want %q", got, want)
	}
}
