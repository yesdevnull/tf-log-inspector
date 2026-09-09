package scrub

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

func TestReloadPreservesDistinctResourcesAndAttribution(t *testing.T) {
	const fixture = `{"@level":"info","@timestamp":"2026-09-08T00:00:00Z","type":"apply_start","hook":{"resource":{"addr":"data.aws_instance.web[\"aa000000-0000-4000-8000-000000000000\"]","resource":"data.aws_instance.web[\"aa000000-0000-4000-8000-000000000000\"]","resource_type":"aws_instance","resource_name":"web","resource_key":"aa000000-0000-4000-8000-000000000000","implied_provider":"aws"},"action":"read"}}
2026-09-08T00:00:00.100Z [TRACE] provider.aws: Sending request downstream: tf_req_id=scope-one tf_rpc=ReadDataSource tf_data_source_type=aws_instance
2026-09-08T00:00:01.000Z [TRACE] provider.aws: Received downstream response: tf_req_id=scope-one tf_req_duration_ms=500 tf_rpc=ReadDataSource tf_data_source_type=aws_instance tf_provider_addr=registry.terraform.io/hashicorp/aws password="LONG_SECRET"
{"@level":"info","@timestamp":"2026-09-08T00:00:02Z","type":"apply_complete","hook":{"resource":{"addr":"data.aws_instance.web[\"aa000000-0000-4000-8000-000000000000\"]","resource_type":"aws_instance","implied_provider":"aws"},"action":"read","elapsed_seconds":2}}
{"@level":"info","@timestamp":"2026-09-08T00:00:02Z","type":"apply_start","hook":{"resource":{"addr":"data.aws_instance.web[\"aA000000-0000-4000-8000-000000000000\"]","resource":"data.aws_instance.web[\"aA000000-0000-4000-8000-000000000000\"]","resource_type":"aws_instance","resource_name":"web","resource_key":"aA000000-0000-4000-8000-000000000000","implied_provider":"aws"},"action":"read"}}
2026-09-08T00:00:02.100Z [TRACE] provider.aws: Sending request downstream: tf_req_id=scope-two tf_rpc=ReadDataSource tf_data_source_type=aws_instance
2026-09-08T00:00:03.000Z [TRACE] provider.aws: Received downstream response: tf_req_id=scope-two tf_req_duration_ms=500 tf_rpc=ReadDataSource tf_data_source_type=aws_instance tf_provider_addr=registry.terraform.io/hashicorp/aws
{"@level":"info","@timestamp":"2026-09-08T00:00:04Z","type":"apply_complete","hook":{"resource":{"addr":"data.aws_instance.web[\"aA000000-0000-4000-8000-000000000000\"]","resource_type":"aws_instance","implied_provider":"aws"},"action":"read","elapsed_seconds":2}}
name=data
{"id":["aa000000-0000-4000-8000-000000000000","aA000000-0000-4000-8000-000000000000"]}
`
	in := strings.Replace(fixture, "LONG_SECRET", strings.Repeat("z", 70000), 1)
	got, err := Scrub([]byte(in), nil)
	if err != nil {
		t.Fatal(err)
	}
	before := loadFixture(t, "original.log", []byte(in))
	after := loadFixture(t, "scrubbed.log", got.Data)
	for _, log := range []*model.Log{before, after} {
		if len(log.Contexts) != 2 || len(log.UISpans) != 2 || len(log.RPCSpans) != 2 {
			t.Fatalf("lost resources/spans: %d/%d/%d", len(log.Contexts), len(log.UISpans), len(log.RPCSpans))
		}
		if log.Contexts[0].Address == log.Contexts[1].Address || log.Contexts[0].Key == log.Contexts[1].Key {
			t.Fatal("case-sensitive resources collapsed")
		}
		for i, c := range log.Contexts {
			if !c.IsData || !strings.HasPrefix(c.Address, "data.aws_instance.") || c.ResourceType != "aws_instance" || c.Action != "read" || c.Unclosed || c.End.Sub(c.Start) != 2*time.Second {
				t.Fatalf("lifecycle changed: %#v", c)
			}
			if log.Attribs[i].Confidence != attrib.Contained || log.Attribs[i].Candidates != 1 || log.Attribs[i].Address != c.Address {
				t.Fatalf("attribution changed: %#v", log.Attribs[i])
			}
			if log.UISpans[i].Address != c.Address || log.UISpans[i].DurationMs != 2000 || log.RPCSpans[i].DurationMs != 500 || len(log.ScopeFor(log.RPCSpans[i].ReqID)) != 2 {
				t.Fatal("scope/span relationships changed")
			}
		}
	}
	if len(before.Entries) != len(after.Entries) {
		t.Fatal("entry count changed")
	}
	for i, e := range before.Entries {
		if e.Level != after.Entries[i].Level || e.TSms != after.Entries[i].TSms {
			t.Fatal("entry order or metadata changed")
		}
	}
	for i, c := range after.Contexts {
		if c.Start != before.Contexts[i].Start || c.End != before.Contexts[i].End || !validGUID.MatchString(strings.Trim(c.Key, `"`)) || strings.Contains(string(got.Data), strings.Trim(before.Contexts[i].Key, `"`)) || !strings.Contains(string(got.Data), c.Key) {
			t.Fatal("mapped identity or lifecycle linkage lost")
		}
	}
}

func loadFixture(t *testing.T, name string, data []byte) *model.Log {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	log, err := model.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return log
}
