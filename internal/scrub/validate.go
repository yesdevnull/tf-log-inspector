package scrub

import (
	"fmt"
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

var parserFields = [...]string{"tf_req_id", "tf_req_duration_ms", "tf_rpc", "tf_resource_type", "tf_data_source_type", "tf_provider_addr"}

type fieldValue struct {
	value   string
	present bool
}
type metadataEntry struct {
	timestamped        bool
	level              logfmt.Level
	time               uint32
	component, request string
	classification     int
	fields             [len(parserFields)]fieldValue
}
type metadataSink struct {
	entries              []metadataEntry
	components, requests *logfmt.Interner
}

func (s *metadataSink) Entry(_ uint32, e logfmt.Entry, msg string, fields logfmt.Fields) {
	m := metadataEntry{timestamped: e.Timestamped, level: e.Level, time: e.TSms, component: s.components.Lookup(e.Comp), request: s.requests.Lookup(e.ReqID)}
	if strings.HasPrefix(msg, "Received downstream response") {
		m.classification = 1
	} else if strings.HasPrefix(msg, "Sending request downstream") {
		m.classification = 2
	}
	for i, key := range parserFields {
		m.fields[i].value, m.fields[i].present = fields.Get(key)
	}
	s.entries = append(s.entries, m)
}

func scanMetadata(text string) ([]metadataEntry, error) {
	var components, requests logfmt.Interner
	s := &metadataSink{components: &components, requests: &requests}
	if _, err := logfmt.Scan(strings.NewReader(text), &components, &requests, s); err != nil {
		return nil, fmt.Errorf("parser at scan: unsupported input")
	}
	return s.entries, nil
}

func (s *session) validate(original, output string) error {
	before, err := scanMetadata(original)
	if err != nil {
		return err
	}
	after, err := scanMetadata(output)
	if err != nil {
		return err
	}
	if len(before) != len(after) {
		return fmt.Errorf("parser at entry 0: changed entry count")
	}
	for i, want := range before {
		want.component, err = s.mappedProvider(want.component, true)
		if err != nil {
			return err
		}
		if want.fields[5].present {
			want.fields[5].value, err = s.mappedProvider(want.fields[5].value, false)
			if err != nil {
				return err
			}
		}
		if want != after[i] {
			return fmt.Errorf("parser at entry %d: changed metadata", i+1)
		}
	}
	return nil
}
