package logfmt

import "testing"

func TestParseHeaderProviderLine(t *testing.T) {
	line := `2022-12-15T00:16:20.800Z [TRACE] provider.terraform-provider-aws_v4.46.0_x5: Received downstream response: tf_req_id=abc tf_req_duration_ms=5`
	h := ParseHeader(line)
	if !h.HasTS {
		t.Fatal("HasTS = false, want true")
	}
	if h.Level != LevelTrace {
		t.Errorf("Level = %v, want TRACE", h.Level)
	}
	if h.Comp != "provider.terraform-provider-aws_v4.46.0_x5" {
		t.Errorf("Comp = %q", h.Comp)
	}
	if h.Msg != "Received downstream response: tf_req_id=abc tf_req_duration_ms=5" {
		t.Errorf("Msg = %q", h.Msg)
	}
	if h.TS.UnixMilli() != 1671063380800 {
		t.Errorf("TS.UnixMilli() = %d, want 1671063380800", h.TS.UnixMilli())
	}
}

func TestParseHeaderNumericOffset(t *testing.T) {
	h := ParseHeader(`2026-08-29T10:34:43.151+0200 [TRACE] terraform.NewContext: complete`)
	if !h.HasTS {
		t.Fatal("HasTS = false, want true")
	}
	if h.Comp != "terraform.NewContext" {
		t.Errorf("Comp = %q, want terraform.NewContext", h.Comp)
	}
	if h.Msg != "complete" {
		t.Errorf("Msg = %q, want complete", h.Msg)
	}
	if _, off := h.TS.Zone(); off != 2*60*60 {
		t.Errorf("zone offset = %d, want 7200", off)
	}
}

func TestParseHeaderPaddedLevelAndNoComponent(t *testing.T) {
	h := ParseHeader(`2026-08-29T10:34:43.123+0200 [INFO]  Terraform version: 1.16.0`)
	if h.Level != LevelInfo {
		t.Errorf("Level = %v, want INFO", h.Level)
	}
	if h.Comp != "" {
		t.Errorf("Comp = %q, want empty (\"Terraform version\" contains a space)", h.Comp)
	}
	if h.Msg != "Terraform version: 1.16.0" {
		t.Errorf("Msg = %q", h.Msg)
	}
}

func TestParseHeaderNoColonMeansNoComponent(t *testing.T) {
	h := ParseHeader(`2026-08-29T10:34:43.151+0200 [TRACE] building graph for terraform dependencies`)
	if h.Comp != "" {
		t.Errorf("Comp = %q, want empty", h.Comp)
	}
	if h.Msg != "building graph for terraform dependencies" {
		t.Errorf("Msg = %q", h.Msg)
	}
}

func TestParseHeaderContinuationLine(t *testing.T) {
	if ParseHeader(`on linux_amd64`).HasTS {
		t.Error("HasTS = true, want false for a continuation line")
	}
}

func TestParseHeaderPlanOutputIsNotAHeader(t *testing.T) {
	if ParseHeader(`  + resource "aws_subnet" "example" {`).HasTS {
		t.Error("HasTS = true, want false for plan output")
	}
}

func TestParseHeaderMultilineValueBodyIsNotAHeader(t *testing.T) {
	// The hclog multi-line value shape, verbatim from testdata/multiline-body.log.
	if ParseHeader(`  | {"__type":"Inva*************tion","message":"Caller is an end user and not allowed to mutate system tags."}`).HasTS {
		t.Error("HasTS = true, want false for an hclog multi-line value body")
	}
}

// Terraform captures a provider's stderr and re-logs each line through its
// own logger, so the provider's hclog header ends up inside Terraform's
// message -- two headers on one line. The provider's level is the
// meaningful one: recording Terraform's wrapper instead hides a provider
// DEBUG line behind an INFO wrapper, which a level filter would then get
// wrong. Measured on a real HCP debug log as 901 INFO entries whose
// providers had said DEBUG.
func TestParseHeaderPeelsNestedProviderHeader(t *testing.T) {
	h := ParseHeader(`2026-09-04T09:15:02.113+1000 [INFO]  provider.terraform-provider-azuread_v3.9.0_x5: 2026-09-04T09:15:03.999+1000 [DEBUG] ==== Begin AzureAD Response ====`)
	if !h.HasTS {
		t.Fatal("HasTS = false, want true")
	}
	if h.Level != LevelDebug {
		t.Errorf("Level = %v, want DEBUG from the nested header", h.Level)
	}
	if h.Comp != "provider.terraform-provider-azuread_v3.9.0_x5" {
		t.Errorf("Comp = %q, want the outer provider component", h.Comp)
	}
	if h.Msg != "==== Begin AzureAD Response ====" {
		t.Errorf("Msg = %q, want the nested header stripped", h.Msg)
	}
	// The outer timestamp is Terraform's, and every other entry in the log
	// is ordered by that clock. The nested one comes from a separate
	// process whose clock may be skewed, so it is read for its level and
	// then discarded.
	if h.TS.Second() != 2 {
		t.Errorf("TS second = %d, want the outer timestamp's 2", h.TS.Second())
	}
}

// A nested header need not carry a level of its own.
func TestParseHeaderPeelsNestedHeaderWithoutLevel(t *testing.T) {
	h := ParseHeader(`2026-09-04T09:15:02.113+1000 [INFO]  provider.x: 2026-09-04T09:15:03.999+1000 plain nested message`)
	if h.Msg != "plain nested message" {
		t.Errorf("Msg = %q, want the nested timestamp stripped", h.Msg)
	}
	if h.Level != LevelInfo {
		t.Errorf("Level = %v, want the outer INFO retained when the nested header has none", h.Level)
	}
}

// Peeling keys off a strictly-formatted timestamp, so ordinary prose that
// merely opens with a bracketed word or a date-like token is untouched.
func TestParseHeaderLeavesUnnestedMessagesAlone(t *testing.T) {
	for _, msg := range []string{
		"Received downstream response: tf_req_duration_ms=5",
		"[not-a-level] keeps its bracket",
		"2026-09-04 09:15:02 is not an hclog timestamp",
		"GET /subscriptions/x/resourceGroups/y HTTP/1.1",
	} {
		h := ParseHeader(`2026-09-04T09:15:02.113+1000 [TRACE] provider.x: ` + msg)
		if h.Msg != msg {
			t.Errorf("Msg = %q, want %q unchanged", h.Msg, msg)
		}
		if h.Level != LevelTrace {
			t.Errorf("Level = %v for %q, want the outer TRACE", h.Level, msg)
		}
	}
}

// The Azure SDK prefixes its messages with a bare "[DEBUG] " rather than a
// full hclog header, and azurerm is the largest component in a real debug
// log by a wide margin -- 7343 entries. Left in place the prefix is masked
// as though it were content, so every one of those templates opens with a
// spurious token. Only recognised level names are peeled; an arbitrary
// bracketed token is left alone, which the sibling test covers.
func TestParseHeaderPeelsBareProviderLevelPrefix(t *testing.T) {
	h := ParseHeader(`2026-09-04T09:15:02.113+1000 [DEBUG] provider.terraform-provider-azurerm_v4.81.0_x5: [DEBUG] AzureRM Request`)
	if h.Msg != "AzureRM Request" {
		t.Errorf("Msg = %q, want the bare level prefix stripped", h.Msg)
	}
	if h.Level != LevelDebug {
		t.Errorf("Level = %v, want DEBUG", h.Level)
	}
	if h.Comp != "provider.terraform-provider-azurerm_v4.81.0_x5" {
		t.Errorf("Comp = %q, want the outer provider component", h.Comp)
	}
}

// A bare bracketed level is stripped for legibility but never believed: a
// message that opens by quoting a level reads identically to a provider's
// own prefix, and relabelling a TRACE entry as ERROR on the strength of its
// text would invent a severity the log never claimed.
func TestParseHeaderBareLevelPrefixDoesNotOverrideLevel(t *testing.T) {
	h := ParseHeader(`2026-09-04T09:15:02.113+1000 [TRACE] provider.x: [ERROR] downstream call failed`)
	if h.Level != LevelTrace {
		t.Errorf("Level = %v, want the outer TRACE kept", h.Level)
	}
	if h.Msg != "downstream call failed" {
		t.Errorf("Msg = %q, want the bracket stripped", h.Msg)
	}
}

// The AzureAD provider logs through Go's standard log package rather than
// hclog, so its nested header carries a "2006/01/02 15:04:05" timestamp --
// two space-separated tokens, which a single-token scan cannot recognise.
// Measured as 901 INFO entries in a real HCP log whose providers had said
// DEBUG, and the reason an earlier peel left that count untouched.
func TestParseHeaderPeelsNestedGoLogHeader(t *testing.T) {
	h := ParseHeader(`2026-09-04T09:15:02.113+1000 [INFO]  provider.terraform-provider-azuread_v3.9.0_x5: 2026/09/04 09:15:03 [DEBUG] performing request`)
	if h.Level != LevelDebug {
		t.Errorf("Level = %v, want DEBUG from the nested Go-log header", h.Level)
	}
	if h.Msg != "performing request" {
		t.Errorf("Msg = %q, want the nested header stripped", h.Msg)
	}
	if h.Comp != "provider.terraform-provider-azuread_v3.9.0_x5" {
		t.Errorf("Comp = %q, want the outer provider component", h.Comp)
	}
	if h.TS.Second() != 2 {
		t.Errorf("TS second = %d, want the outer timestamp's 2", h.TS.Second())
	}
}

// Go's log package prints a fractional second when configured with
// Lmicroseconds, and time.Parse accepts one after the seconds field even
// though the layout does not name it.
func TestParseHeaderPeelsNestedGoLogHeaderWithMicroseconds(t *testing.T) {
	h := ParseHeader(`2026-09-04T09:15:02.113+1000 [INFO]  provider.x: 2026/09/04 09:15:03.123456 [WARN] throttled`)
	if h.Level != LevelWarn {
		t.Errorf("Level = %v, want WARN", h.Level)
	}
	if h.Msg != "throttled" {
		t.Errorf("Msg = %q, want the nested header stripped", h.Msg)
	}
}

// The leniency is for nested headers only. Accepting a Go-log timestamp at
// the start of a line would promote continuation text -- a provider's
// multi-line body, or interleaved plan output -- into a logical entry of
// its own, splitting entries that belong together.
func TestParseHeaderRejectsGoLogTimestampAsAnEntryHeader(t *testing.T) {
	if ParseHeader(`2026/09/04 09:15:03 [DEBUG] this is a continuation line`).HasTS {
		t.Error("HasTS = true, want false: a Go-log timestamp does not open an hclog entry")
	}
}
