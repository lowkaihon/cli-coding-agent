package tools

// skipDirs defines directory names that file-walking tools (glob, grep) should
// ignore during traversal. These are typically large, generated, or version-control
// directories that are not useful for code search.
var skipDirs = map[string]bool{
	".git":        true,
	"node_modules": true,
	".venv":       true,
	"__pycache__": true,
}

// shouldSkipDir reports whether a directory should be skipped during file traversal.
func shouldSkipDir(name string) bool {
	return skipDirs[name]
}
