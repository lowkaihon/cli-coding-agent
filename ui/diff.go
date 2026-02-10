package ui

import (
	"fmt"
	"strings"
)

// PrintDiff prints a colorized unified diff.
func (t *Terminal) PrintDiff(path, oldContent, newContent string) {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	fmt.Println(t.c(Bold, fmt.Sprintf("--- %s", path)))
	fmt.Println(t.c(Bold, fmt.Sprintf("+++ %s", path)))

	// Simple line-by-line diff — find changed region
	// For the edit tool, we know the change is localized, so a simple approach works.
	maxLen := len(oldLines)
	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}

	// Find first differing line
	start := 0
	for start < len(oldLines) && start < len(newLines) && oldLines[start] == newLines[start] {
		start++
	}

	// Find last differing line (from end)
	endOld := len(oldLines) - 1
	endNew := len(newLines) - 1
	for endOld > start && endNew > start && oldLines[endOld] == newLines[endNew] {
		endOld--
		endNew--
	}

	// Print context before
	contextLines := 3
	from := start - contextLines
	if from < 0 {
		from = 0
	}

	fmt.Println(t.c(Cyan, fmt.Sprintf("@@ -%d,%d +%d,%d @@", from+1, endOld-from+1, from+1, endNew-from+1)))

	for i := from; i < start; i++ {
		fmt.Println(t.c(Gray, " "+oldLines[i]))
	}

	// Print removed lines
	for i := start; i <= endOld && i < len(oldLines); i++ {
		fmt.Println(t.c(Red, "-"+oldLines[i]))
	}

	// Print added lines
	for i := start; i <= endNew && i < len(newLines); i++ {
		fmt.Println(t.c(Green, "+"+newLines[i]))
	}

	// Print context after
	to := endOld + contextLines + 1
	if to > len(oldLines) {
		to = len(oldLines)
	}
	for i := endOld + 1; i < to; i++ {
		fmt.Println(t.c(Gray, " "+oldLines[i]))
	}
}

// PrintFilePreview prints a preview of file contents for the write tool.
func (t *Terminal) PrintFilePreview(path, content string) {
	fmt.Println(t.c(Bold+Green, fmt.Sprintf("New file: %s", path)))
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		fmt.Println(t.c(Gray, fmt.Sprintf("  %3d │ ", i+1)) + t.c(Green, line))
	}
}

// ConfirmAction asks the user for y/n confirmation.
func (t *Terminal) ConfirmAction(prompt string) bool {
	fmt.Print(t.c(Bold+Yellow, prompt+" [y/n] "))
	var response string
	fmt.Scanln(&response)
	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes"
}
