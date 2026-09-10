package model

import (
	"fmt"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

const (
	resourceBenchmarkAddresses       = 10_000
	resourceBenchmarkUIPerAddress    = 3
	resourceBenchmarkRPCs            = 100_000
	resourceBenchmarkSelectedAddress = 42
	resourceBenchmarkSelectedModule  = 42
)

var (
	benchmarkResourceIndex      ResourceIndex
	benchmarkResourceProjection ResourceProjection
)

type resourceBenchmarkFixture struct {
	log             *Log
	index           ResourceIndex
	selectedAddress string
	selectedModule  string
	wantAddressUI   int
	wantAddressRPC  int
	wantModuleUI    int
	wantModuleRPC   int
}

func newResourceBenchmarkFixture() resourceBenchmarkFixture {
	addresses := make([]string, resourceBenchmarkAddresses)
	modules := make([]string, resourceBenchmarkAddresses)
	l := &Log{
		UISpans:  make([]span.Span, 0, resourceBenchmarkAddresses*resourceBenchmarkUIPerAddress),
		RPCSpans: make([]span.Span, resourceBenchmarkRPCs),
		Attribs:  make([]attrib.Attribution, resourceBenchmarkRPCs),
	}
	for i := range resourceBenchmarkAddresses {
		module := fmt.Sprintf("module.group[%d]", i%100)
		address := fmt.Sprintf("%s.aws_instance.item_%05d", module, i)
		addresses[i] = address
		modules[i] = module
		for _, action := range [...]string{"create", "update", "delete"} {
			l.UISpans = append(l.UISpans, span.Span{
				Address:      address,
				Module:       module,
				ModuleKnown:  true,
				ResourceType: "aws_instance",
				RPC:          action,
				DurationMs:   uint32(i%100 + 1),
			})
		}
	}
	l.Contexts = []attrib.Context{{
		Address:     addresses[0],
		Module:      modules[0],
		ModuleKnown: true,
	}}

	for i := range resourceBenchmarkRPCs {
		l.RPCSpans[i] = span.Span{
			Provider:        "provider-a",
			RPC:             "ReadResource",
			ResourceType:    "aws_instance",
			DurationMs:      uint32(i%50 + 1),
			TimestampStatus: logfmt.TimestampValid,
		}
		if i%2 != 0 {
			l.RPCSpans[i].Provider = "provider-b"
		}
		addressIndex := i % resourceBenchmarkAddresses
		switch i % 5 {
		case 0:
			l.Attribs[i] = attrib.Attribution{Address: addresses[addressIndex], Module: modules[addressIndex], ModuleKnown: true, Confidence: attrib.Contained}
		case 1:
			l.Attribs[i] = attrib.Attribution{Address: addresses[addressIndex], Module: modules[addressIndex], ModuleKnown: true, Confidence: attrib.Likely}
		case 2:
			l.Attribs[i] = attrib.Attribution{Address: addresses[addressIndex], Module: modules[addressIndex], ModuleKnown: true, Confidence: attrib.Overlapping}
		case 3:
			l.Attribs[i] = attrib.Attribution{Confidence: attrib.Ambiguous, Candidates: 2}
		default:
			l.Attribs[i] = attrib.Attribution{Confidence: attrib.Unattributed}
		}
	}

	return resourceBenchmarkFixture{
		log:             l,
		index:           BuildResourceIndex(l),
		selectedAddress: addresses[resourceBenchmarkSelectedAddress],
		selectedModule:  modules[resourceBenchmarkSelectedModule],
		wantAddressUI:   resourceBenchmarkUIPerAddress,
		wantAddressRPC:  resourceBenchmarkRPCs / resourceBenchmarkAddresses,
		wantModuleUI:    resourceBenchmarkAddresses / 100 * resourceBenchmarkUIPerAddress,
		wantModuleRPC:   resourceBenchmarkRPCs / 100,
	}
}

func (f resourceBenchmarkFixture) report(b *testing.B) {
	b.Helper()
	b.ReportMetric(resourceBenchmarkAddresses, "fixture_addresses")
	b.ReportMetric(resourceBenchmarkAddresses*resourceBenchmarkUIPerAddress, "fixture_ui")
	b.ReportMetric(resourceBenchmarkRPCs, "fixture_rpcs")
}

func BenchmarkResourceBuildIndex(b *testing.B) {
	fixture := newResourceBenchmarkFixture()
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		got := BuildResourceIndex(fixture.log)
		if len(got.Operations) != resourceBenchmarkAddresses*resourceBenchmarkUIPerAddress || len(got.RPCModules) != resourceBenchmarkRPCs || len(got.Choices) != resourceBenchmarkAddresses {
			b.Fatalf("index sizes = operations %d, RPC modules %d, choices %d", len(got.Operations), len(got.RPCModules), len(got.Choices))
		}
		benchmarkResourceIndex = got
	}
	fixture.report(b)
}

func BenchmarkResourceSelect(b *testing.B) {
	fixture := newResourceBenchmarkFixture()
	cases := []struct {
		name    string
		base    Filter
		named   ResourceSelection
		wantUI  int
		wantRPC int
	}{
		{
			name:    "Unconstrained",
			wantUI:  resourceBenchmarkAddresses * resourceBenchmarkUIPerAddress,
			wantRPC: resourceBenchmarkRPCs,
		},
		{
			name:    "ProviderOnly",
			base:    Filter{Providers: map[string]bool{"provider-a": true}},
			wantUI:  resourceBenchmarkAddresses * resourceBenchmarkUIPerAddress,
			wantRPC: resourceBenchmarkRPCs / 2,
		},
		{
			name:    "ResourceOnly",
			named:   ResourceSelection{Addresses: map[string]bool{fixture.selectedAddress: true}},
			wantUI:  fixture.wantAddressUI,
			wantRPC: fixture.wantAddressRPC,
		},
		{
			name:    "ModuleOnly",
			named:   ResourceSelection{Modules: map[string]bool{fixture.selectedModule: true}},
			wantUI:  fixture.wantModuleUI,
			wantRPC: fixture.wantModuleRPC,
		},
		{
			name:    "ProviderAndResource",
			base:    Filter{Providers: map[string]bool{"provider-a": true}},
			named:   ResourceSelection{Addresses: map[string]bool{fixture.selectedAddress: true}},
			wantUI:  fixture.wantAddressUI,
			wantRPC: fixture.wantAddressRPC,
		},
		{
			name:    "ProviderAndModule",
			base:    Filter{Providers: map[string]bool{"provider-a": true}},
			named:   ResourceSelection{Modules: map[string]bool{fixture.selectedModule: true}},
			wantUI:  fixture.wantModuleUI,
			wantRPC: fixture.wantModuleRPC,
		},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				got := SelectResources(fixture.log, fixture.index, tc.base, tc.named)
				if len(got.UIIndices) != tc.wantUI || len(got.RPCIndices) != tc.wantRPC {
					b.Fatalf("projection sizes = UI %d, RPC %d; want UI %d, RPC %d", len(got.UIIndices), len(got.RPCIndices), tc.wantUI, tc.wantRPC)
				}
				benchmarkResourceProjection = got
			}
			fixture.report(b)
		})
	}
}

func BenchmarkResourceSelectBroadModules(b *testing.B) {
	for _, moduleCount := range []int{10, 1_000} {
		b.Run(fmt.Sprintf("Modules%d", moduleCount), func(b *testing.B) {
			l := &Log{UISpans: make([]span.Span, 0, moduleCount*10)}
			selected := make(map[string]bool, moduleCount)
			for moduleIndex := range moduleCount {
				module := fmt.Sprintf("module.group[%d]", moduleIndex)
				selected[module] = true
				for resourceIndex := range 10 {
					l.UISpans = append(l.UISpans, span.Span{
						Address:     fmt.Sprintf("%s.aws_instance.item_%d", module, resourceIndex),
						Module:      module,
						ModuleKnown: true,
						DurationMs:  1,
					})
				}
			}
			index := BuildResourceIndex(l)
			selection := ResourceSelection{Modules: selected}
			wantUI := moduleCount * 10
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				got := SelectResources(l, index, Filter{}, selection)
				if len(got.UIIndices) != wantUI || len(got.Rows) != wantUI {
					b.Fatalf("projection sizes = UI %d, rows %d; want %d", len(got.UIIndices), len(got.Rows), wantUI)
				}
				benchmarkResourceProjection = got
			}
		})
	}
}
