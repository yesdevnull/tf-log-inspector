package logfmt

import (
	"math"
	"testing"
	"time"
)

func TestRelativePositionClassifiesClockValidity(t *testing.T) {
	origin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		at   time.Time
		base time.Time
		want ClockPosition
	}{
		{name: "missing timestamp", base: origin, want: ClockPosition{Status: TimestampMissing}},
		{name: "missing origin", at: origin, want: ClockPosition{Status: TimestampMissing}},
		{name: "genuine zero", at: origin, base: origin, want: ClockPosition{Status: TimestampValid}},
		{name: "sub-millisecond", at: origin.Add(999 * time.Microsecond), base: origin, want: ClockPosition{Status: TimestampValid}},
		{name: "before origin by one nanosecond", at: origin.Add(-time.Nanosecond), base: origin, want: ClockPosition{Status: TimestampBeforeOrigin}},
		{name: "maximum", at: origin.Add(time.Duration(math.MaxUint32) * time.Millisecond), base: origin, want: ClockPosition{OffsetMs: math.MaxUint32, Status: TimestampValid}},
		{name: "past maximum", at: origin.Add((time.Duration(math.MaxUint32) + 1) * time.Millisecond), base: origin, want: ClockPosition{Status: TimestampOutOfRange}},
		{name: "distant date", at: origin.AddDate(100, 0, 0), base: origin, want: ClockPosition{Status: TimestampOutOfRange}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RelativePosition(tc.at, tc.base); got != tc.want {
				t.Errorf("RelativePosition() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
