package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

type bashInput struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

const (
	defaultTimeout = 30
	maxTimeout     = 120
	maxOutputChars = 10000
)

func (r *Registry) bashTool(ctx context.Context, input json.RawMessage) (string, error) {
	var params bashInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if params.Command == "" {
		return "", fmt.Errorf("command is required")
	}

	timeout := params.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if timeout > maxTimeout {
		timeout = maxTimeout
	}

	return "", &NeedsConfirmation{
		Tool:    "bash",
		Path:    params.Command,
		Preview: params.Command,
		Execute: func() (string, error) {
			timeoutDur := time.Duration(timeout) * time.Second
			execCtx, cancel := context.WithTimeout(ctx, timeoutDur)
			defer cancel()

			var cmd *exec.Cmd
			if runtime.GOOS == "windows" {
				cmd = exec.CommandContext(execCtx, "cmd", "/C", params.Command)
			} else {
				cmd = exec.CommandContext(execCtx, "bash", "-c", params.Command)
			}
			cmd.Dir = r.workDir

			var buf bytes.Buffer
			cmd.Stdout = &buf
			cmd.Stderr = &buf

			err := cmd.Run()

			output := buf.String()
			truncated := false
			if len(output) > maxOutputChars {
				output = output[:maxOutputChars]
				truncated = true
			}

			var result string
			if err != nil {
				if execCtx.Err() == context.DeadlineExceeded {
					result = fmt.Sprintf("Command timed out after %ds.\n%s", timeout, output)
				} else {
					result = fmt.Sprintf("Exit code: %s\n%s", err, output)
				}
			} else {
				result = output
				if result == "" {
					result = "(no output)"
				}
			}

			if truncated {
				result += "\n[output truncated]"
			}

			return result, nil
		},
	}
}
