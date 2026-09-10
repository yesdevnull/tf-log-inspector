package profile

import (
	"fmt"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func BenchmarkJSONProjection(b *testing.B) {
	for _, size := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("observations-%d", size), func(b *testing.B) {
			report := generatedBenchmarkReport(size)
			metadata := JSONMetadata{ToolVersion: "benchmark", InputBasename: "sanitised.log"}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := buildJSONProfile(report, metadata); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func generatedBenchmarkReport(size int) Report {
	r := Report{HasContext: true, Reconstruction: model.ReconstructionQuality{State: "not_checked"}}
	r.Quality.HasContext = true
	r.Quality.Attribution.Candidates = map[uint32]int{1: size}
	r.RPC = make([]Observation, size)
	r.Providers = make([]model.Bucket, size)
	r.Types = make([]TypeSummary, size)
	for i := range size {
		name := fmt.Sprintf("sanitised_%d", i)
		r.RPC[i] = Observation{Index: i, Span: span.Span{Entry: uint32(i), DurationMs: uint32(i), RPC: "ReadResource", Provider: name, ResourceType: name}, Attribution: attrib.Attribution{Confidence: attrib.Contained, Address: name, Candidates: 1}}
		r.Providers[i] = model.Bucket{Key: name, Count: 1, TotalMs: uint64(i), MaxMs: uint32(i)}
		r.Types[i] = TypeSummary{TypeRow: model.TypeRow{ResourceType: name, RPCCalls: 1, RPCTotalMs: uint64(i), RPCMaxMs: uint32(i)}}
	}
	return r
}
