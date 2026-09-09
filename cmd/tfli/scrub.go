package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yesdevnull/tf-log-inspector/internal/scrub"
)

var scrubCategories = []string{"name", "id", "guid", "email", "network", "cloud", "path", "secret", "explicit"}

var openScrubOutput = func(path string) (io.WriteCloser, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

func runScrub(inputPath, outputPath, valuesPath string, stderr io.Writer) error {
	input, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", inputPath, err)
	}
	var values []string
	if valuesPath != "" {
		values, err = readScrubValues(valuesPath)
		if err != nil {
			return err
		}
	}
	result, err := scrub.Scrub(input, values)
	if err != nil {
		return fmt.Errorf("scrubbing %s: %w", inputPath, err)
	}
	out, err := openScrubOutput(outputPath)
	if err != nil {
		return fmt.Errorf("creating %s: %w", outputPath, err)
	}
	if len(result.Data) > 0 {
		n, writeErr := out.Write(result.Data)
		if writeErr == nil && n != len(result.Data) {
			writeErr = io.ErrShortWrite
		}
		if writeErr != nil {
			closeErr := out.Close()
			cause := fmt.Errorf("writing %s: %w", outputPath, writeErr)
			if closeErr != nil {
				cause = errors.Join(cause, fmt.Errorf("closing %s: %w", outputPath, closeErr))
			}
			return removePartialScrubOutput(outputPath, cause)
		}
	}
	if closeErr := out.Close(); closeErr != nil {
		return removePartialScrubOutput(outputPath, fmt.Errorf("closing %s: %w", outputPath, closeErr))
	}
	fmt.Fprintln(stderr, "Replacement counts:")
	for _, category := range scrubCategories {
		fmt.Fprintf(stderr, "  %s: %d\n", category, result.Replacements[category])
	}
	fmt.Fprintf(stderr, "Unsupported structured or quoted inputs: %d\n", result.Unsupported)
	if len(result.UnsupportedInputs) > 0 {
		fmt.Fprintf(stderr, "Locations in the original input (showing first %d; 1-based character columns):\n", len(result.UnsupportedInputs))
		for _, input := range result.UnsupportedInputs {
			fmt.Fprintf(stderr, "  line %d, column %d: %s\n", input.Line, input.Column, input.Reason)
		}
		if remaining := result.Unsupported - len(result.UnsupportedInputs); remaining > 0 {
			fmt.Fprintf(stderr, "%d additional instances omitted.\n", remaining)
		}
	}
	fmt.Fprintln(stderr, "Review the scrubbed log before sharing it.")
	return nil
}

func removePartialScrubOutput(path string, cause error) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(cause, fmt.Errorf("removing partial output %s: %w", path, err))
	}
	return cause
}

func readScrubValues(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading values file %s: %w", path, err)
	}
	lineNumber := 1
	for rest := data; len(rest) > 0; {
		r, size := utf8.DecodeRune(rest)
		if r == utf8.RuneError && size == 1 {
			return nil, invalidScrubValue(lineNumber)
		}
		if r == '\n' {
			lineNumber++
		}
		rest = rest[size:]
	}
	lines := strings.Split(string(data), "\n")
	values := make([]string, 0, len(lines))
	for i, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		if strings.ContainsFunc(line, unicode.IsControl) {
			return nil, invalidScrubValue(i + 1)
		}
		values = append(values, line)
	}
	return values, nil
}

func invalidScrubValue(line int) error {
	return fmt.Errorf("values file at line %d: invalid value", line)
}
