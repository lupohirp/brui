package bru_test

import (
	"runtime"
	"testing"

	"github.com/lupohirp/brui/pkg/bru"
)

func TestLoadCollectionAndMemory(t *testing.T) {
	coll, err := bru.LoadCollection("/home/pakilodi/TIM/bruno-collections/APIGEE")
	if err != nil {
		t.Fatalf("failed to load collection: %v", err)
	}

	if coll.Name == "" {
		t.Errorf("expected collection name, got empty")
	}

	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	allocMB := float64(m.Alloc) / 1024 / 1024
	sysMB := float64(m.Sys) / 1024 / 1024

	t.Logf("Collection Name: %s", coll.Name)
	t.Logf("Environments: %d", len(coll.Environments))
	t.Logf("Folders: %d", len(coll.Folders))
	t.Logf("RAM Alloc: %.2f MB", allocMB)
	t.Logf("RAM Sys: %.2f MB", sysMB)
}
