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
