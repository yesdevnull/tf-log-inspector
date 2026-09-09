package logfmt

import (
	"math"
	"time"
)

type TimestampStatus uint8

const (
	TimestampMissing TimestampStatus = iota
	TimestampValid
	TimestampInvalid
	TimestampBeforeOrigin
	TimestampOutOfRange
)

type ClockPosition struct {
	OffsetMs uint32
	Status   TimestampStatus
}

type ClockSink interface {
	EntryClock(ord uint32, position ClockPosition)
}

func RelativePosition(at, origin time.Time) ClockPosition {
	if at.IsZero() || origin.IsZero() {
		return ClockPosition{Status: TimestampMissing}
	}
	if at.Before(origin) {
		return ClockPosition{Status: TimestampBeforeOrigin}
	}
	ms := at.Sub(origin).Milliseconds()
	if ms > math.MaxUint32 {
		return ClockPosition{Status: TimestampOutOfRange}
	}
	return ClockPosition{OffsetMs: uint32(ms), Status: TimestampValid}
}
