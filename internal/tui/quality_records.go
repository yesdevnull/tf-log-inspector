package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

type qualityItemID struct {
	kind        string
	stage, code string
	index       int
}

type qualityRecord struct {
	id         qualityItemID
	text       string
	sourceLine uint64
}

type qualityActionRow struct {
	id         qualityItemID
	start, end int
	sourceLine uint64
}

type qualityNavigationState struct {
	open     bool
	selected qualityItemID
	offset   int
}

func diagnosticSourceLine(d logfmt.ProviderJSONDiagnostic) uint64 {
	for _, line := range []int{d.SyntaxLine, d.Line, d.StartLine} {
		if line > 0 {
			return uint64(line)
		}
	}
	return 0
}

func diagnosticQualityRecords(diagnostics []logfmt.ProviderJSONDiagnostic) []qualityRecord {
	records := make([]qualityRecord, 0, len(diagnostics))
	for i, d := range diagnostics {
		line := diagnosticSourceLine(d)
		location := "location unavailable"
		if line != 0 {
			location = fmt.Sprintf("source line %d", line)
			if d.SyntaxLine == 0 && d.StartLine > 0 && uint64(d.StartLine) != line {
				location += fmt.Sprintf(", response starts at line %d", d.StartLine)
			}
		}
		records = append(records, qualityRecord{
			id: qualityItemID{kind: "diagnostic", index: i}, sourceLine: line,
			text: fmt.Sprintf("  %s: %s", logfmt.DisplayText(d.Code), location),
		})
	}
	sort.SliceStable(records, func(i, j int) bool {
		a, b := records[i].sourceLine, records[j].sourceLine
		if a == 0 || b == 0 {
			if a == b {
				return records[i].id.index < records[j].id.index
			}
			return b == 0
		}
		if a != b {
			return a < b
		}
		return records[i].id.index < records[j].id.index
	})
	return records
}

func (m *Model) qualityRecords(q model.CaptureQuality, reconstruction model.ReconstructionQuality) []qualityRecord {
	records := []qualityRecord{{id: qualityItemID{kind: "check"}, text: "Check responses"}}
	if m.inspection.running && reconstruction.State == "not_checked" {
		records[0].text += " — checking responses…"
		records = append(records, qualityRecord{text: "Closing this panel leaves the check running."})
	}
	add := func(text string) { records = append(records, qualityRecord{text: text}) }
	add("TIMING AVAILABILITY")
	var b strings.Builder
	writeQualityTier(&b, "RPC", q.RPC)
	writeQualityTier(&b, "UI", q.UI)
	for _, line := range strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n") {
		add(line)
	}
	add("")
	add("ANOMALIES")
	add("  Counts can overlap across stages and must not be totalled as bad lines.")
	add("  First samples use original one-based physical source lines; they are not exhaustive.")
	hasIssue := false
	for _, issue := range q.Issues {
		hasIssue = hasIssue || issue.Count != 0
	}
	if !hasIssue {
		add("  none recorded")
	}
	stage := ""
	for _, issue := range q.Issues {
		if issue.Count == 0 {
			continue
		}
		if issue.Stage != stage {
			stage = issue.Stage
			add(fmt.Sprintf("  %s — %s", logfmt.DisplayText(stage), qualityStageUnits(stage)))
		}
		text := fmt.Sprintf("    %s: %d", logfmt.DisplayText(issue.Code), issue.Count)
		line := uint64(0)
		if issue.FirstEntry != nil {
			if location, ok := m.log.SourceLocation(*issue.FirstEntry); ok {
				line = location.StartLine
				text += fmt.Sprintf(" (first at %s, line %d", logfmt.DisplayText(m.name), location.StartLine)
				if location.EndLine != location.StartLine {
					text += fmt.Sprintf("-%d", location.EndLine)
				}
				text += ")"
			} else {
				text += " (location unavailable)"
			}
		} else {
			text += " (location unavailable)"
		}
		records = append(records, qualityRecord{id: qualityItemID{kind: "anomaly", stage: issue.Stage, code: issue.Code}, text: text, sourceLine: line})
	}
	add("")
	add("ATTRIBUTION")
	if !q.HasContext {
		add("  unavailable: no address context was recognised")
		add(fmt.Sprintf("  nameable duration: unavailable / %dms (no address context)", q.RPCDurationMs))
	} else {
		if q.NameableShare == nil {
			add("  nameable duration: unavailable (total RPC duration is 0ms)")
		} else {
			add(fmt.Sprintf("  nameable duration: %dms / %dms (%.1f%%)", q.NameableMs, q.RPCDurationMs, *q.NameableShare*100))
		}
		add(fmt.Sprintf("  attributed spans: %d / %d", q.Attribution.ByConfidence[attrib.Contained]+q.Attribution.ByConfidence[attrib.Likely], q.Attribution.Spans))
	}
	for _, c := range []attrib.Confidence{attrib.Contained, attrib.Likely, attrib.Overlapping, attrib.Ambiguous, attrib.Unattributed} {
		add(fmt.Sprintf("  %-12s %d spans, %dms", c.String(), q.Attribution.ByConfidence[c], q.Attribution.MsByConfidence[c]))
	}
	add("")
	add("CONTEXT LIMITATIONS")
	if hasQualityIssue(q.Issues, "context", "context_incomplete") {
		add("  Context evidence is incomplete; this limits attribution and does not establish that an operation failed.")
	} else if q.HasContext {
		add("  no incomplete contexts recorded")
	} else {
		add("  no address context was recognised")
	}
	add("  Recognition requires structured records to carry literal @level and @timestamp keys.")
	add("  Malformed hclog headers are not recovered as independent RPC records.")
	add("")
	add("RECONSTRUCTION")
	switch reconstruction.State {
	case "complete":
		add(fmt.Sprintf("  complete: %d responses available", reconstruction.Responses))
	case "partial":
		add(fmt.Sprintf("  partial: %d responses available; %d reconstruction diagnostics", reconstruction.Responses, reconstruction.Diagnostics))
		add("  Diagnostic counts can include ownership triggers and aborted messages.")
	case "failed":
		add(fmt.Sprintf("  failed: 0 responses available; %d reconstruction diagnostics", reconstruction.Diagnostics))
		add("  Diagnostic counts can include ownership triggers and aborted messages.")
	default:
		add("  not checked (response reconstruction is lazy)")
	}
	if reconstruction.State != "not_checked" {
		records = append(records, diagnosticQualityRecords(m.log.ReconstructionDiagnostics())...)
	}
	return records
}

func wrapQualityRecords(records []qualityRecord, width int) ([]string, []qualityActionRow) {
	prefixWidth := min(2, max(0, width))
	prefix := strings.Repeat(" ", prefixWidth)
	wrapWidth := max(1, width-prefixWidth)
	var lines []string
	var actions []qualityActionRow
	for _, record := range records {
		start := len(lines)
		for _, line := range strings.Split(ansi.Wrap(record.text, wrapWidth, ""), "\n") {
			lines = append(lines, clipWidth(prefix+line, max(0, width)))
		}
		if record.id.kind != "" {
			actions = append(actions, qualityActionRow{id: record.id, start: start, end: len(lines), sourceLine: record.sourceLine})
		}
	}
	return lines, actions
}
