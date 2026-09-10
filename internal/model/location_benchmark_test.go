package model

import (
	"bufio"
	"fmt"
	"os"
	"testing"
)

const benchmarkFixtureEntries = 16_384

var (
	benchmarkLog      *Log
	benchmarkLocation SourceLocation
)

type sourceLocationBenchmarkFixture struct {
	path          string
	bytes         int64
	entries       int
	physicalLines int
	target        uint32
	expected      SourceLocation
}

func (f sourceLocationBenchmarkFixture) report(b *testing.B) {
	b.Helper()
	b.ReportMetric(float64(f.bytes), "fixture_bytes")
	b.ReportMetric(float64(f.entries), "fixture_entries")
	b.ReportMetric(float64(f.physicalLines), "fixture_lines")
}

func newSourceLocationBenchmarkFixture(b *testing.B) sourceLocationBenchmarkFixture {
	b.Helper()

	path := b.TempDir() + "/large.log"
	file, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	writer := bufio.NewWriter(file)
	for i := 0; i < benchmarkFixtureEntries; i++ {
		_, err = fmt.Fprintf(writer,
			"2026-09-10T00:00:00.000Z [TRACE] provider.terraform-provider-example_v1.0.0_x5: Received downstream response: tf_req_id=%08d-0000-4000-8000-000000000000 tf_resource_type=example_item tf_rpc=ReadResource tf_proto_version=5.3 tf_provider_addr=registry.terraform.io/example/example tf_req_duration_ms=1 @module=sdk.proto\n  continuation payload %08d alpha beta gamma delta\n  continuation payload %08d epsilon zeta eta theta\n",
			i, i, i,
		)
		if err != nil {
			b.Fatal(err)
		}
	}
	if err := writer.Flush(); err != nil {
		b.Fatal(err)
	}
	if err := file.Close(); err != nil {
		b.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		b.Fatal(err)
	}
	if len(loaded.Entries) != benchmarkFixtureEntries {
		b.Fatalf("loaded %d entries, want %d", len(loaded.Entries), benchmarkFixtureEntries)
	}
	target := uint32(len(loaded.Entries) - 1)
	expected, ok := loaded.SourceLocation(target)
	if !ok {
		b.Fatalf("source location for entry %d not found", target)
	}

	return sourceLocationBenchmarkFixture{
		path:          path,
		bytes:         info.Size(),
		entries:       len(loaded.Entries),
		physicalLines: benchmarkFixtureEntries * 3,
		target:        target,
		expected:      expected,
	}
}

func BenchmarkLoad(b *testing.B) {
	fixture := newSourceLocationBenchmarkFixture(b)
	b.ReportAllocs()
	b.SetBytes(fixture.bytes)
	b.ResetTimer()
	fixture.report(b)

	for i := 0; i < b.N; i++ {
		loaded, err := Load(fixture.path)
		if err != nil {
			b.Fatal(err)
		}
		if len(loaded.Entries) != fixture.entries {
			b.Fatalf("loaded %d entries, want %d", len(loaded.Entries), fixture.entries)
		}
		benchmarkLog = loaded
	}
}

func BenchmarkSourceLocationFirst(b *testing.B) {
	fixture := newSourceLocationBenchmarkFixture(b)
	loaded, err := Load(fixture.path)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	fixture.report(b)

	for i := 0; i < b.N; i++ {
		fresh := &Log{Data: loaded.Data, Entries: loaded.Entries}
		location, ok := fresh.SourceLocation(fixture.target)
		if !ok || location != fixture.expected {
			b.Fatalf("source location = (%+v, %t), want (%+v, true)", location, ok, fixture.expected)
		}
		benchmarkLocation = location
	}
}

func BenchmarkSourceLocationRepeated(b *testing.B) {
	fixture := newSourceLocationBenchmarkFixture(b)
	loaded, err := Load(fixture.path)
	if err != nil {
		b.Fatal(err)
	}
	location, ok := loaded.SourceLocation(fixture.target)
	if !ok || location != fixture.expected {
		b.Fatalf("source location = (%+v, %t), want (%+v, true)", location, ok, fixture.expected)
	}
	b.ReportAllocs()
	b.ResetTimer()
	fixture.report(b)

	for i := 0; i < b.N; i++ {
		location, ok = loaded.SourceLocation(fixture.target)
		if !ok || location != fixture.expected {
			b.Fatalf("source location = (%+v, %t), want (%+v, true)", location, ok, fixture.expected)
		}
		benchmarkLocation = location
	}
}
