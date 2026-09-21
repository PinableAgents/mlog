package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestExamples(t *testing.T) {
	t.Chdir(t.TempDir())
	main()
	paths := []string{"single_file/1001/single_file_service/all.log", "custom_name/2001/custom_app/application.log", "multi_file/3001/multi_file_service/info.log", "structured/4001/structured_service/structured.log", "business/5001/business_service/all.log"}
	for _, p := range paths {
		b, e := os.ReadFile(filepath.Join("example_logs", p))
		if e != nil || len(b) == 0 {
			t.Fatal(p, e)
		}
	}
	b, e := os.ReadFile("example_logs/structured/4001/structured_service/structured.log")
	if e != nil || !bytes.Contains(b, []byte(`"user_id":12345`)) {
		t.Fatal("example lost structured fields", e)
	}
}
