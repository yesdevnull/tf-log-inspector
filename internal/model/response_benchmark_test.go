package model

import (
	"fmt"
	"strings"
	"testing"
)

var providerResponseSelectionSink ProviderResponseSelection

func BenchmarkProviderResponseAtFirst(b *testing.B) {
	for _, groups := range []int{1_000, 10_000} {
		b.Run(fmt.Sprintf("groups-%d", groups), func(b *testing.B) {
			loaded := loadResponseBenchmarkLog(b, providerResponseBenchmarkSource(groups))
			positions := responseBenchmarkPositions(b, loaded, uint64(groups*3-2), uint64(groups*3-1), uint64(groups*3))
			states := []string{"complete", "invalid", "unavailable"}
			for i, position := range positions {
				fresh := &Log{Data: loaded.Data, Entries: loaded.Entries}
				if got := fresh.ProviderResponseAt(position.entry, position.lineOffset); got.State != states[i] {
					b.Fatalf("validation selection %d = %+v", i, got)
				}
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				fresh := &Log{Data: loaded.Data, Entries: loaded.Entries}
				position := positions[i%len(positions)]
				providerResponseSelectionSink = fresh.ProviderResponseAt(position.entry, position.lineOffset)
			}
		})
	}
}

func BenchmarkProviderResponseAtRepeated(b *testing.B) {
	for _, groups := range []int{1_000, 10_000} {
		b.Run(fmt.Sprintf("groups-%d", groups), func(b *testing.B) {
			loaded := loadResponseBenchmarkLog(b, providerResponseBenchmarkSource(groups))
			positions := responseBenchmarkPositions(b, loaded, uint64(groups*3-2), uint64(groups*3-1), uint64(groups*3))
			states := []string{"complete", "invalid", "unavailable"}
			for i, position := range positions {
				got := loaded.ProviderResponseAt(position.entry, position.lineOffset)
				assertResponseBenchmarkSelection(b, got, states[i])
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				position := positions[i%len(positions)]
				providerResponseSelectionSink = loaded.ProviderResponseAt(position.entry, position.lineOffset)
			}
		})
	}
}

func BenchmarkProviderResponseAtFailureTail(b *testing.B) {
	const head = "2026-09-11T00:00:00.000Z [DEBUG] provider."
	for _, tailLines := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("local-%d", tailLines), func(b *testing.B) {
			source := head + "a: {\"broken\":]}\n" + strings.Repeat(head+"a: {\"apparent_restart\":1}\n", tailLines)
			loaded := loadResponseBenchmarkLog(b, source)
			positions := responseBenchmarkPositions(b, loaded, 1, uint64(tailLines+1))
			loaded.inspectProviderResponses()
			if len(loaded.responseDiagnostics) != 1 || len(loaded.responseDiagnostics[0].Unavailable) != tailLines {
				b.Fatalf("local fixture diagnostics = %#v", loaded.responseDiagnostics)
			}
			for i, tc := range []struct {
				name, state string
			}{{"invalid", "invalid"}, {"unavailable", "unavailable"}} {
				position := positions[i]
				assertResponseBenchmarkSelection(b, loaded.ProviderResponseAt(position.entry, position.lineOffset), tc.state)
				b.Run(tc.name, func(b *testing.B) {
					runRepeatedResponseBenchmark(b, loaded, []responseBenchmarkPosition{position})
				})
			}
		})

		b.Run(fmt.Sprintf("global-%d", tailLines), func(b *testing.B) {
			source := head + "a: {\"pending\":\n" + head + ": {\"unknown\":1}\n" + strings.Repeat("ordinary tail\n", tailLines)
			loaded := loadResponseBenchmarkLog(b, source)
			positions := responseBenchmarkPositions(b, loaded, 1, 2, uint64(tailLines+2))
			loaded.inspectProviderResponses()
			if len(loaded.responseDiagnostics) != 2 || len(loaded.responseDiagnostics[1].Unavailable) != tailLines+1 {
				b.Fatalf("global fixture diagnostics = %#v", loaded.responseDiagnostics)
			}
			for i, tc := range []struct {
				name, state string
			}{{"aborted-invalid", "invalid"}, {"trigger-unavailable", "unavailable"}, {"tail-unavailable", "unavailable"}} {
				position := positions[i]
				assertResponseBenchmarkSelection(b, loaded.ProviderResponseAt(position.entry, position.lineOffset), tc.state)
				b.Run(tc.name, func(b *testing.B) {
					runRepeatedResponseBenchmark(b, loaded, []responseBenchmarkPosition{position})
				})
			}
		})
	}
}

type responseBenchmarkPosition struct {
	entry      uint32
	lineOffset int
}

func providerResponseBenchmarkSource(groups int) string {
	const head = "2026-09-11T00:00:00.000Z [DEBUG] provider."
	var source strings.Builder
	for i := range groups {
		fmt.Fprintf(&source, "%sa: {\"good\":%d}\n", head, i)
		fmt.Fprintf(&source, "%sb%d: {\"broken\":]}\n", head, i)
		fmt.Fprintf(&source, "%sb%d: {\"apparent_restart\":1}\n", head, i)
	}
	return source.String()
}

func loadResponseBenchmarkLog(b *testing.B, source string) *Log {
	b.Helper()
	return loadResponseLog(b, source)
}

type responseTesting interface {
	Helper()
	Fatalf(string, ...any)
}

func responseBenchmarkPositions(t responseTesting, l *Log, lines ...uint64) []responseBenchmarkPosition {
	t.Helper()
	positions := make([]responseBenchmarkPosition, 0, len(lines))
	for _, line := range lines {
		found := false
		for entry := range l.Entries {
			location, ok := l.SourceLocation(uint32(entry))
			if ok && line >= location.StartLine && line <= location.EndLine {
				positions = append(positions, responseBenchmarkPosition{entry: uint32(entry), lineOffset: int(line - location.StartLine)})
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("source line %d has no entry", line)
		}
	}
	return positions
}

func assertResponseBenchmarkSelection(b *testing.B, got ProviderResponseSelection, state string) {
	b.Helper()
	if got.State != state {
		b.Fatalf("selection = %+v, want state %q", got, state)
	}
	if state != "complete" && (got.Diagnostic == nil || got.Diagnostic.Ranges != nil || got.Diagnostic.Unavailable != nil) {
		b.Fatalf("status diagnostic = %#v", got.Diagnostic)
	}
}

func runRepeatedResponseBenchmark(b *testing.B, l *Log, positions []responseBenchmarkPosition) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		position := positions[i%len(positions)]
		providerResponseSelectionSink = l.ProviderResponseAt(position.entry, position.lineOffset)
	}
}
