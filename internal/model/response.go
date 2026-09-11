package model

import (
	"sort"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

// ProviderResponseSelection describes reconstruction at one physical source line.
type ProviderResponseSelection struct {
	State          string
	SourceLine     uint64
	Response       logfmt.ProviderJSON
	Diagnostic     *logfmt.ProviderJSONDiagnostic
	HasDiagnostics bool
}

type responseRange struct {
	start uint64
	end   uint64
	index int
}

// ProviderResponseAt returns reconstruction status at one physical source line.
func (l *Log) ProviderResponseAt(entry uint32, lineOffset int) ProviderResponseSelection {
	start, end, line, ok := l.responseSourceLine(entry, lineOffset)
	selection := ProviderResponseSelection{State: "none"}
	if !ok {
		return selection
	}

	l.InspectProviderResponses()
	selection.SourceLine = line
	selection.HasDiagnostics = len(l.responseDiagnostics) != 0
	if i, ok := responseRangeMatch(l.responseMessageRanges, start, end); ok {
		selection.State = "complete"
		selection.Response = l.responses[i]
		selection.Response.Fragments = append([]logfmt.JSONFragment(nil), selection.Response.Fragments...)
		return selection
	}
	for _, candidate := range []struct {
		state  string
		ranges []responseRange
	}{
		{state: "invalid", ranges: l.responseFailedRanges},
		{state: "unavailable", ranges: l.responseUnavailableRanges},
	} {
		if i, ok := responseRangeMatch(candidate.ranges, start, end); ok {
			diagnostic := l.responseDiagnostics[i]
			diagnostic.Ranges = nil
			diagnostic.Unavailable = nil
			selection.State = candidate.state
			selection.Diagnostic = &diagnostic
			return selection
		}
	}
	return selection
}

// InspectProviderResponses reconstructs and indexes provider responses.
func (l *Log) InspectProviderResponses() {
	l.responseOnce.Do(func() {
		result := logfmt.InspectProviderJSON(string(l.Data))
		l.responses = result.Messages
		l.responseDiagnostics = result.Diagnostics
		l.responseMessageRanges = providerResponseRanges(l.responses)
		l.responseFailedRanges = providerDiagnosticRanges(l.responseDiagnostics, false)
		l.responseUnavailableRanges = providerDiagnosticRanges(l.responseDiagnostics, true)
		l.responseChecked.Store(true)
	})
}

// ReconstructionDiagnostics returns published content-free diagnostic metadata.
func (l *Log) ReconstructionDiagnostics() []logfmt.ProviderJSONDiagnostic {
	if !l.responseChecked.Load() {
		return nil
	}
	out := append([]logfmt.ProviderJSONDiagnostic(nil), l.responseDiagnostics...)
	for i := range out {
		out[i].Ranges = nil
		out[i].Unavailable = nil
	}
	return out
}

func providerResponseRanges(responses []logfmt.ProviderJSON) []responseRange {
	var ranges []responseRange
	for i, response := range responses {
		for _, fragment := range response.Fragments {
			if fragment.Start != fragment.End {
				ranges = append(ranges, responseRange{start: uint64(fragment.Start), end: uint64(fragment.End), index: i})
			}
		}
	}
	sortResponseRanges(ranges)
	return ranges
}

func providerDiagnosticRanges(diagnostics []logfmt.ProviderJSONDiagnostic, unavailable bool) []responseRange {
	var ranges []responseRange
	for i, diagnostic := range diagnostics {
		fragments := diagnostic.Ranges
		if unavailable {
			fragments = diagnostic.Unavailable
		}
		for _, fragment := range fragments {
			if fragment.Start != fragment.End {
				ranges = append(ranges, responseRange{start: uint64(fragment.Start), end: uint64(fragment.End), index: i})
			}
		}
	}
	sortResponseRanges(ranges)
	return ranges
}

func sortResponseRanges(ranges []responseRange) {
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].start != ranges[j].start {
			return ranges[i].start < ranges[j].start
		}
		if ranges[i].end != ranges[j].end {
			return ranges[i].end < ranges[j].end
		}
		return ranges[i].index < ranges[j].index
	})
}

func (l *Log) responseSourceLine(entry uint32, lineOffset int) (start, end, line uint64, ok bool) {
	location, ok := l.SourceLocation(entry)
	if !ok || lineOffset < 0 || uint64(lineOffset) > location.EndLine-location.StartLine {
		return 0, 0, 0, false
	}

	line = location.StartLine + uint64(lineOffset)
	lineIndex := line - 1
	if lineIndex >= uint64(len(l.sourceLineStarts)) {
		return 0, 0, 0, false
	}
	start = l.sourceLineStarts[lineIndex]
	end = uint64(len(l.Data))
	if lineIndex+1 < uint64(len(l.sourceLineStarts)) {
		end = l.sourceLineStarts[lineIndex+1]
	}
	if start < location.StartByte {
		start = location.StartByte
	}
	if end > location.EndByte {
		end = location.EndByte
	}
	if end > start && l.Data[end-1] == '\n' {
		end--
		if end > start && l.Data[end-1] == '\r' {
			end--
		}
	} else if end > start && l.Data[end-1] == '\r' {
		end--
	}
	return start, end, line, true
}

func responseRangeMatch(ranges []responseRange, start, end uint64) (int, bool) {
	if start >= end {
		return 0, false
	}
	i := sort.Search(len(ranges), func(i int) bool { return ranges[i].end > start })
	if i == len(ranges) || ranges[i].start >= end {
		return 0, false
	}
	return ranges[i].index, true
}
