package span

import "github.com/yesdevnull/tf-log-inspector/internal/logfmt"

type IssueCount struct {
	Count      uint64
	FirstEntry uint32
}

type TimingEvidence struct {
	Records         uint64
	Rejected        map[string]IssueCount
	SyntaxErrors    IssueCount
	SchemaErrors    IssueCount
	TimestampIssues map[string]IssueCount
}

func addIssue(issues map[string]IssueCount, reason string, ord uint32) {
	issue := issues[reason]
	if issue.Count == 0 {
		issue.FirstEntry = ord
	}
	issue.Count++
	issues[reason] = issue
}

func addStage(issue *IssueCount, ord uint32) {
	if issue.Count == 0 {
		issue.FirstEntry = ord
	}
	issue.Count++
}

func detachedEvidence(e TimingEvidence) TimingEvidence {
	e.Rejected = cloneIssues(e.Rejected)
	e.TimestampIssues = cloneIssues(e.TimestampIssues)
	return e
}

func cloneIssues(src map[string]IssueCount) map[string]IssueCount {
	dst := make(map[string]IssueCount, len(src))
	for reason, issue := range src {
		dst[reason] = issue
	}
	return dst
}

func timestampReason(status logfmt.TimestampStatus) string {
	switch status {
	case logfmt.TimestampMissing:
		return "timestamp_missing"
	case logfmt.TimestampInvalid:
		return "timestamp_invalid"
	case logfmt.TimestampBeforeOrigin:
		return "timestamp_before_origin"
	case logfmt.TimestampOutOfRange:
		return "timestamp_out_of_range"
	default:
		return ""
	}
}
