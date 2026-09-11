package main

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/profile"
)

type comparisonOptions struct {
	Format string
	Text   profile.TextOptions
}

func runComparison(beforePath, afterPath, outPath string, stdout io.Writer, options comparisonOptions) error {
	beforeLog, err := model.Load(beforePath)
	if err != nil {
		return fmt.Errorf("loading before: %w", err)
	}
	afterLog, err := model.Load(afterPath)
	if err != nil {
		return fmt.Errorf("loading after: %w", err)
	}
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
	return writeReport(stdout, []string{beforePath, afterPath}, outPath, render)
}
