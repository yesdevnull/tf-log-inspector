package span

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/resourceaddr"
)

var cliDurationPattern = regexp.MustCompile(`^(?:([0-9]+)h)?(?:([0-9]+)m)?(?:([0-9]+(?:\.[0-9]+)?)s)?$`)

func (b *UIHookBuilder) suppressCLI() bool { return b.structuredLifecycle || b.timestampedLog }

// CLICompletion receives only complete physical lines from the scanner. The
// final capture-wide admission check is shared by model and diagnose callers.
func (b *UIHookBuilder) CLICompletion(ord uint32, e logfmt.Entry, line string) {
	address, action, duration, ok := logfmt.CLICompletionParts(line)
	if !ok {
		return
	}
	b.cliEvidence.Records++
	reject := func(code string) {
		if b.cliEvidence.Rejected == nil {
			b.cliEvidence.Rejected = make(map[string]IssueCount)
		}
		addIssue(b.cliEvidence.Rejected, code, ord)
	}
	parsed, validAddress := resourceaddr.Parse(address)
	if !validAddress {
		reject("cli_address_invalid")
		return
	}
	if before, suffix, found := strings.Cut(duration, " ["); found {
		// IDs are opaque and sanitised captures may truncate their suffix.
		// Only the duration token contributes timing evidence.
		if !strings.HasPrefix(suffix, "id=") {
			reject("cli_duration_invalid")
			return
		}
		duration = before
	}
	ms, saturated, valid := cliDuration(duration)
	if !valid {
		reject("cli_duration_invalid")
		return
	}
	actions := map[string]string{"Read": "read", "Creation": "create", "Modifications": "update", "Destruction": "delete", "Refresh": "refresh"}
	resourceType := parsed.Type
	provider, _, _ := strings.Cut(resourceType, "_")
	b.cliSpans = append(b.cliSpans, Span{
		Entry: ord, DurationMs: ms, DurationSource: SourceCLIElapsed,
		TimestampStatus: logfmt.TimestampMissing, DurationSaturated: saturated,
		RPC: b.kept.retain(actions[action]), Fidelity: FidelityUIReported,
		Address: strings.Clone(address), Module: parsed.Module, ModuleKnown: true,
		ResourceType: b.kept.retain(resourceType), Provider: b.kept.retain(provider),
	})
}

func cliDuration(value string) (uint32, bool, bool) {
	parts := cliDurationPattern.FindStringSubmatch(value)
	if parts == nil || value == "" {
		return 0, false, false
	}
	var ms float64
	for i, multiplier := range []float64{3600000, 60000, 1000} {
		if parts[i+1] == "" {
			continue
		}
		n, err := strconv.ParseFloat(parts[i+1], 64)
		if err != nil && !math.IsInf(n, 1) {
			return 0, false, false
		}
		ms += n * multiplier
	}
	ms = math.Round(ms)
	if ms > math.MaxUint32 {
		return math.MaxUint32, true, true
	}
	return uint32(ms), false, true
}

func mergeIssue(issues map[string]IssueCount, code string, addition IssueCount) {
	issue := issues[code]
	if issue.Count == 0 || addition.FirstEntry < issue.FirstEntry {
		issue.FirstEntry = addition.FirstEntry
	}
	issue.Count += addition.Count
	issues[code] = issue
}
