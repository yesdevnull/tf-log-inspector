package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/yesdevnull/tf-log-inspector/internal/profile"
)

type comparisonOptions struct {
	Format string
	Text   profile.TextOptions
}

func runComparison(beforePath, afterPath, outPath string, stdout io.Writer, options comparisonOptions) error {
	beforeFile, beforeLog, err := loadReportInput(beforePath)
	if err != nil {
		return fmt.Errorf("loading before: %w", err)
	}
	defer func() { _ = beforeFile.Close() }()
	afterFile, afterLog, err := loadReportInput(afterPath)
	if err != nil {
		return fmt.Errorf("loading after: %w", err)
	}
	defer func() { _ = afterFile.Close() }()
	before, err := profile.Build(beforeLog)
	if err != nil {
		return fmt.Errorf("profiling before: %w", err)
	}
	after, err := profile.Build(afterLog)
	if err != nil {
		return fmt.Errorf("profiling after: %w", err)
	}
	report, err := profile.BuildComparison(before, after)
	if err != nil {
		return fmt.Errorf("comparing captures: %w", err)
	}
	metadata := profile.ComparisonMetadata{ToolVersion: version, BeforeBasename: filepath.Base(beforePath), AfterBasename: filepath.Base(afterPath)}
	render := func(w io.Writer) error {
		if options.Format == "json" {
			return profile.RenderComparisonJSON(w, report, metadata)
		}
		return profile.RenderComparisonText(w, report, metadata, options.Text)
	}
	return writeReport(stdout, []*os.File{beforeFile, afterFile}, outPath, render)
}
