package logfmt

import "fmt"

// ProviderJSONResult contains verified provider messages and safe reconstruction
// diagnostics.
type ProviderJSONResult struct {
	Messages    []ProviderJSON
	Diagnostics []ProviderJSONDiagnostic
}

// ProviderJSONDiagnostic describes a reconstruction failure without retaining
// source content.
type ProviderJSONDiagnostic struct {
	Code               string
	Line, StartLine    int
	FragmentCount      int
	JoinedBytes        int
	FirstFragmentBytes int
	LastFragmentBytes  int
	SyntaxOffset       int64
	SyntaxLine         int
	Ranges             []JSONFragment
	Unavailable        []JSONFragment
}

// ReconstructProviderJSON returns messages only when inspection has no
// diagnostics. Strict consumers therefore never receive a partial result.
func ReconstructProviderJSON(text string) ([]ProviderJSON, error) {
	result := InspectProviderJSON(text)
	if len(result.Diagnostics) != 0 {
		return nil, result.Diagnostics[0]
	}
	return result.Messages, nil
}

// Error formats a content-free reconstruction failure.
func (d ProviderJSONDiagnostic) Error() string {
	if d.Code == "incomplete" {
		return fmt.Sprintf("provider JSON at line %d: incomplete or invalid body", d.StartLine)
	}
	reason := ""
	switch d.Code {
	case "delimiter_mismatch":
		reason = "delimiter mismatch"
	case "suffix_grammar":
		reason = "suffix grammar"
	case "json_syntax":
		reason = "JSON syntax"
	case "invalid_utf8":
		reason = "UTF-8"
	case "invalid_inline_ui":
		reason = "invalid inline UI envelope"
	case "ambiguous_ownership":
		return fmt.Sprintf("provider JSON at line %d: ambiguous ownership", d.Line)
	default:
		return "provider JSON: reconstruction failed"
	}
	err := providerJSONFailure(reason, d.Line, d.StartLine, d.FragmentCount, d.JoinedBytes, d.FirstFragmentBytes, d.LastFragmentBytes, d.SyntaxOffset)
	if d.SyntaxLine != 0 {
		return fmt.Sprintf("%v; syntax source line %d", err, d.SyntaxLine)
	}
	return err.Error()
}

func newProviderJSONDiagnostic(reason string, line, startLine, fragments, bytes, firstBytes, lastBytes int, syntaxOffset int64, ranges []JSONFragment) ProviderJSONDiagnostic {
	code := ""
	switch reason {
	case "delimiter mismatch":
		code = "delimiter_mismatch"
	case "suffix grammar":
		code = "suffix_grammar"
	case "JSON syntax":
		code = "json_syntax"
	case "UTF-8":
		code = "invalid_utf8"
	case "invalid inline UI envelope":
		code = "invalid_inline_ui"
	}
	diagnostic := ProviderJSONDiagnostic{
		Code:               code,
		Line:               line,
		StartLine:          startLine,
		FragmentCount:      fragments,
		JoinedBytes:        bytes,
		FirstFragmentBytes: firstBytes,
		LastFragmentBytes:  lastBytes,
		SyntaxOffset:       syntaxOffset,
		Ranges:             append([]JSONFragment(nil), ranges...),
	}
	remaining := syntaxOffset
	for _, fragment := range ranges {
		if remaining > 0 && remaining <= int64(fragment.End-fragment.Start) {
			diagnostic.SyntaxLine = fragment.Line
			break
		}
		remaining -= int64(fragment.End - fragment.Start)
	}
	return diagnostic
}

func providerJSONFailure(reason string, line, startLine, fragments, bytes, firstBytes, lastBytes int, syntaxOffset int64) error {
	detail := fmt.Sprintf("%s; start line %d; fragments %d; joined bytes %d; first fragment bytes %d; last fragment bytes %d", reason, startLine, fragments, bytes, firstBytes, lastBytes)
	if syntaxOffset > 0 {
		detail += fmt.Sprintf("; syntax offset %d", syntaxOffset)
	}
	return fmt.Errorf("provider JSON at line %d: invalid body (%s)", line, detail)
}

func completedProviderJSON(messages []ProviderJSON) []ProviderJSON {
	return completedProviderJSONExcept(messages, -1)
}

func completedProviderJSONExcept(messages []ProviderJSON, excluded int) []ProviderJSON {
	completed := make([]ProviderJSON, 0, len(messages))
	for i, message := range messages {
		if i == excluded {
			continue
		}
		if message.Text != "" {
			completed = append(completed, message)
		}
	}
	return completed
}

func totalProviderJSONBytes(fragments []JSONFragment) int {
	total := 0
	for _, fragment := range fragments {
		total += fragment.End - fragment.Start
	}
	return total
}
