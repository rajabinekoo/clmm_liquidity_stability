package utils

import (
	"reflect"
	"strings"
	"testing"
)

func TestSharedConfigDoesNotRequireGraphAPIKey(t *testing.T) {
	t.Parallel()

	field, ok := reflect.TypeOf(Config{}).FieldByName("TheGraphAPIKey")
	if !ok {
		t.Fatal("Config.TheGraphAPIKey field is missing")
	}

	if got := field.Tag.Get("env"); got != "THE_GRAPH_API_KEY" {
		t.Fatalf("TheGraphAPIKey env tag = %q, want %q", got, "THE_GRAPH_API_KEY")
	}
}

func TestConfigValidateIndexerRequiresGraphAPIKey(t *testing.T) {
	t.Parallel()

	err := (Config{}).ValidateIndexer()
	if err == nil || !strings.Contains(err.Error(), "THE_GRAPH_API_KEY") {
		t.Fatalf("ValidateIndexer() error = %v, want missing graph API key", err)
	}
}

func TestNormalizedTargetImpactBpsValues(t *testing.T) {
	t.Parallel()

	values, err := (Config{NormalizedTargetImpactBps: "1,5,10,25,50"}).NormalizedTargetImpactBpsValues()
	if err != nil {
		t.Fatalf("NormalizedTargetImpactBpsValues() error = %v", err)
	}
	if len(values) != 5 || values[0].String() != "1" || values[4].String() != "50" {
		t.Fatalf("values = %v", values)
	}
}

func TestNormalizedTargetImpactBpsValuesRejectsUnorderedTargets(t *testing.T) {
	t.Parallel()

	_, err := (Config{NormalizedTargetImpactBps: "1,10,5"}).NormalizedTargetImpactBpsValues()
	if err == nil || !strings.Contains(err.Error(), "strictly increasing") {
		t.Fatalf("NormalizedTargetImpactBpsValues() error = %v, want ordering error", err)
	}
}

func TestNormalizedGridMaxOutputQuantizationBpsValue(t *testing.T) {
	t.Parallel()

	value, err := (Config{
		NormalizedGridMaxOutputQuantizationBps: "0.1",
	}).NormalizedGridMaxOutputQuantizationBpsValue()
	if err != nil {
		t.Fatalf("NormalizedGridMaxOutputQuantizationBpsValue() error = %v", err)
	}
	if value.String() != "0.1" {
		t.Fatalf("value = %s, want 0.1", value)
	}
}

func TestNormalizedGridMaxOutputQuantizationBpsValueRejectsUnsafeBound(t *testing.T) {
	t.Parallel()

	_, err := (Config{
		NormalizedGridMaxOutputQuantizationBps: "2",
	}).NormalizedGridMaxOutputQuantizationBpsValue()
	if err == nil || !strings.Contains(err.Error(), "at most 1") {
		t.Fatalf("error = %v, want upper-bound validation", err)
	}
}
