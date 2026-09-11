package logfmt

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkInspectProviderJSON(b *testing.B) {
	for _, distinct := range []bool{false, true} {
		for _, count := range []int{1000, 10000} {
			b.Run(fmt.Sprintf("distinct=%t/count=%d", distinct, count), func(b *testing.B) {
				var source strings.Builder
				for i := 0; i < count; i++ {
					a, damaged := "a", "b"
					if distinct {
						a, damaged = fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i)
					}
					source.WriteString(providerRecord(a, `{"ok":1}`) + "\n")
					source.WriteString(providerRecord(damaged, `{"bad":]}`) + "\n")
					source.WriteString(providerRecord(damaged, `{"apparent":1}`) + "\n")
				}
				input := source.String()
				checked := InspectProviderJSON(input)
				wantDiagnostics := 1
				if distinct {
					wantDiagnostics = count
				}
				if len(checked.Messages) != count || len(checked.Diagnostics) != wantDiagnostics {
					b.Fatal("invalid benchmark outcome")
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					InspectProviderJSON(input)
				}
			})
		}
	}
}
