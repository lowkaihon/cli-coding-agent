package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Create some test files
	os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n\nfunc main() {}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "hello_test.go"), []byte("package main\n\nfunc TestMain() {}\n"), 0644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "sub", "nested.go"), []byte("package sub\n\nvar x = 42\n"), 0644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# Hello\nWorld\n"), 0644)
	return dir
}

func TestGlobTool(t *testing.T) {
	dir := setupTestDir(t)
	r := NewRegistry(dir)

	tests := []struct {
		name    string
		pattern string
		want    []string
		noMatch bool
	}{
		{"all go files", "**/*.go", []string{"hello.go", "hello_test.go", "sub/nested.go"}, false},
		{"test files only", "**/*_test.go", []string{"hello_test.go"}, false},
		{"top-level go files", "*.go", []string{"hello.go", "hello_test.go"}, false},
		{"nested only", "sub/*.go", []string{"sub/nested.go"}, false},
		{"no match", "**/*.rs", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, _ := json.Marshal(globInput{Pattern: tt.pattern})
			result, err := r.Execute(context.Background(), "glob", input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.noMatch {
				if !strings.Contains(result, "No files matched") {
					t.Errorf("expected no match message, got: %s", result)
				}
				return
			}
			for _, want := range tt.want {
				if !strings.Contains(result, want) {
					t.Errorf("expected %q in result, got: %s", want, result)
				}
			}
		})
	}
}

func TestGrepTool(t *testing.T) {
	dir := setupTestDir(t)
	r := NewRegistry(dir)

	tests := []struct {
		name    string
		pattern string
		include string
		want    string
		noMatch bool
	}{
		{"find func", "func main", "", "hello.go:3", false},
		{"find var", "var x", "", "sub/nested.go:3", false},
		{"with include filter", "package", "*.md", "", true},
		{"no match", "nonexistent_string_xyz", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, _ := json.Marshal(grepInput{Pattern: tt.pattern, Include: tt.include})
			result, err := r.Execute(context.Background(), "grep", input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.noMatch {
				if !strings.Contains(result, "No matches") {
					t.Errorf("expected no match, got: %s", result)
				}
				return
			}
			if !strings.Contains(result, tt.want) {
				t.Errorf("expected %q in result, got: %s", tt.want, result)
			}
		})
	}
}

func TestReadTool(t *testing.T) {
	dir := setupTestDir(t)
	r := NewRegistry(dir)

	tests := []struct {
		name      string
		path      string
		startLine int
		endLine   int
		want      string
		wantErr   bool
	}{
		{"read whole file", "hello.go", 0, 0, "func main()", false},
		{"read line range", "hello.go", 1, 1, "package main", false},
		{"file not found", "nonexistent.txt", 0, 0, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, _ := json.Marshal(readInput{Path: tt.path, StartLine: tt.startLine, EndLine: tt.endLine})
			result, err := r.Execute(context.Background(), "read", input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(result, tt.want) {
				t.Errorf("expected %q in result, got: %s", tt.want, result)
			}
		})
	}
}

func TestLsTool(t *testing.T) {
	dir := setupTestDir(t)
	r := NewRegistry(dir)

	input, _ := json.Marshal(lsInput{})
	result, err := r.Execute(context.Background(), "ls", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"hello.go", "sub/"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result, got: %s", want, result)
		}
	}
}

func TestValidatePath(t *testing.T) {
	dir := t.TempDir()

	// Use an absolute path that is definitely outside the temp dir
	outsidePath := filepath.Join(os.TempDir(), "definitely_outside", "nope.txt")

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"relative valid", "foo.txt", false},
		{"nested valid", "sub/foo.txt", false},
		{"traversal attack", "../../etc/passwd", true},
		{"absolute outside", outsidePath, true},
		{"absolute inside", filepath.Join(dir, "inside.txt"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidatePath(dir, tt.path)
			if tt.wantErr && err == nil {
				t.Error("expected error for path traversal")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestWriteToolNeedsConfirmation(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir)

	input, _ := json.Marshal(writeInput{Path: "newfile.txt", Content: "hello world"})
	_, err := r.Execute(context.Background(), "write", input)
	if err == nil {
		t.Fatal("expected NeedsConfirmation error")
	}

	confirm, ok := err.(*NeedsConfirmation)
	if !ok {
		t.Fatalf("expected *NeedsConfirmation, got %T: %v", err, err)
	}
	if confirm.Tool != "write" {
		t.Errorf("expected tool=write, got %s", confirm.Tool)
	}

	// Execute the confirmation
	result, err := confirm.Execute()
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(result, "Successfully wrote") {
		t.Errorf("unexpected result: %s", result)
	}

	// Verify file was created
	data, err := os.ReadFile(filepath.Join(dir, "newfile.txt"))
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("unexpected content: %s", string(data))
	}
}

func TestEditToolNeedsConfirmation(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello world"), 0644)
	r := NewRegistry(dir)

	input, _ := json.Marshal(editInput{Path: "test.txt", OldStr: "hello", NewStr: "goodbye"})
	_, err := r.Execute(context.Background(), "edit", input)
	if err == nil {
		t.Fatal("expected NeedsConfirmation error")
	}

	confirm, ok := err.(*NeedsConfirmation)
	if !ok {
		t.Fatalf("expected *NeedsConfirmation, got %T: %v", err, err)
	}

	result, err := confirm.Execute()
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(result, "Successfully edited") {
		t.Errorf("unexpected result: %s", result)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	if string(data) != "goodbye world" {
		t.Errorf("unexpected content: %s", string(data))
	}
}

func TestEditToolNoMatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello world"), 0644)
	r := NewRegistry(dir)

	input, _ := json.Marshal(editInput{Path: "test.txt", OldStr: "nonexistent", NewStr: "replacement"})
	_, err := r.Execute(context.Background(), "edit", input)
	if err == nil {
		t.Fatal("expected error for no match")
	}
	if _, ok := err.(*NeedsConfirmation); ok {
		t.Fatal("should not get NeedsConfirmation for no match")
	}
}

func TestEditToolMultipleMatches(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("aaa\naaa\n"), 0644)
	r := NewRegistry(dir)

	input, _ := json.Marshal(editInput{Path: "test.txt", OldStr: "aaa", NewStr: "bbb"})
	_, err := r.Execute(context.Background(), "edit", input)
	if err == nil {
		t.Fatal("expected error for multiple matches")
	}
	if !strings.Contains(err.Error(), "matches 2 times") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBashToolNeedsConfirmation(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir)

	input, _ := json.Marshal(bashInput{Command: "echo hello"})
	_, err := r.Execute(context.Background(), "bash", input)
	if err == nil {
		t.Fatal("expected NeedsConfirmation error")
	}

	confirm, ok := err.(*NeedsConfirmation)
	if !ok {
		t.Fatalf("expected *NeedsConfirmation, got %T: %v", err, err)
	}
	if confirm.Tool != "bash" {
		t.Errorf("expected tool=bash, got %s", confirm.Tool)
	}

	result, err := confirm.Execute()
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("expected hello in output, got: %s", result)
	}
}

func TestIsReadOnly(t *testing.T) {
	r := NewRegistry(t.TempDir())

	readOnlyTools := []string{"glob", "grep", "ls", "read", "write_tasks", "update_task", "read_tasks"}
	for _, name := range readOnlyTools {
		if !r.IsReadOnly(name) {
			t.Errorf("expected %s to be read-only", name)
		}
	}

	writeTools := []string{"write", "edit", "bash"}
	for _, name := range writeTools {
		if r.IsReadOnly(name) {
			t.Errorf("expected %s to NOT be read-only", name)
		}
	}
}
