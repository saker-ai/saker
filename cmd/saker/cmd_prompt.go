package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cinience/saker/pkg/api"
)

func resolvePrompt(literal, file string, tail []string) (string, error) {
	if strings.TrimSpace(literal) != "" {
		return literal, nil
	}
	if strings.TrimSpace(file) != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read prompt file: %w", err)
		}
		return string(data), nil
	}
	if len(tail) > 0 {
		return strings.Join(tail, " "), nil
	}
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", errors.New("no prompt provided")
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

func shouldAutoEnterInteractive(literal, file string, tail []string, printMode, acp bool) bool {
	if acp || printMode {
		return false
	}
	if strings.TrimSpace(literal) != "" || strings.TrimSpace(file) != "" || len(tail) > 0 {
		return false
	}
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func printResponse(resp *api.Response, out io.Writer) {
	if resp == nil || out == nil {
		return
	}
	fmt.Fprintf(out, "# saker run (%s)\n", resp.Mode.EntryPoint)
	if resp.Result != nil {
		fmt.Fprintf(out, "stop_reason: %s\n", resp.Result.StopReason)
		fmt.Fprintf(out, "output:\n%s\n", resp.Result.Output)
	}
}

func streamRunJSON(ctx context.Context, rt runtimeClient, req api.Request, out, errOut io.Writer, verbose bool) error {
	ch, err := rt.RunStream(ctx, req)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	for evt := range ch {
		if verbose && errOut != nil {
			switch evt.Type {
			case api.EventToolExecutionResult, api.EventMessageStop, api.EventError:
				_, _ = fmt.Fprintf(errOut, "[event] %s\n", evt.Type)
			}
		}
		if err := encoder.Encode(evt); err != nil {
			return err
		}
	}
	return nil
}
