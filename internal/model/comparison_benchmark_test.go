package model

import (
	"fmt"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func BenchmarkCompare(b *testing.B) {
	for _, size := range []int{1000, 10000} {
		for _, distinct := range []bool{false, true} {
			b.Run(fmt.Sprintf("n=%d/distinct=%t", size, distinct), func(b *testing.B) {
				input := ComparisonInput{RPC: make([]span.Span, size), UI: make([]span.Span, size)}
				for i := range size {
					key := "shared"
					if distinct {
						key = fmt.Sprintf("sanitised_%d", i)
					}
					input.RPC[i] = span.Span{Provider: key, ResourceType: key, RPC: "Read", DurationMs: 10}
					input.UI[i] = span.Span{Address: key, ResourceType: key, RPC: "read", DurationMs: 1000}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if _, err := Compare(input, input); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
