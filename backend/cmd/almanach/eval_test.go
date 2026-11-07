package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadEnvFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.env")
	body := "# keys\n\nA=1\nexport B=\"two words\"\nC='3'\nnot a pair\n  D = 4 \n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "1", "B": "two words", "C": "3", "D": "4"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("readEnvFile = %v, want %v", got, want)
	}
	if _, err := readEnvFile(filepath.Join(t.TempDir(), "missing.env")); err == nil {
		t.Error("a missing file: want an error")
	}
}
