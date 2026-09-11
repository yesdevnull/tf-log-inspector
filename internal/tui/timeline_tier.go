package tui

import (
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func timelineTierFor(l *model.Log) timelineTier {
	fidelity, ok := model.PreferredTiming(l)
	if !ok {
		return tierNone
	}
	if fidelity == span.FidelityUIReported {
		return tierUI
	}
	return tierRPC
}

func (m *Model) activeTimelineTier() timelineTier {
	if m.timeline.tier != tierNone {
		return m.timeline.tier
	}
	return timelineTierFor(m.log)
}

func (m *Model) rememberTimelineSelection() {
	tier := m.activeTimelineTier()
	if tier != tierNone {
		m.timeline.selections[tier] = m.selectedTimelineIdentity()
	}
}

func (m *Model) reconcileTimelineSelection() {
	id := m.timeline.selections[m.activeTimelineTier()]
	if !m.restoreTimelineIdentity(id) {
		m.timeline.lane, m.timeline.span = 0, 0
		m.clampTimelineSelection()
	}
	m.rememberTimelineSelection()
}

func (m *Model) switchTimelineTier() {
	hasRPC, hasUI := len(m.log.RPCSpans) > 0, len(m.log.UISpans) > 0
	if !hasRPC || !hasUI {
		switch {
		case hasRPC:
			m.timeline.notice = "RPC timing only; UI timing unavailable"
		case hasUI:
			m.timeline.notice = "UI timing only; RPC timing unavailable"
		default:
			m.timeline.notice = "No RPC or UI timing observations"
		}
		return
	}
	m.rememberTimelineSelection()
	if m.activeTimelineTier() == tierRPC {
		m.timeline.tier = tierUI
	} else {
		m.timeline.tier = tierRPC
	}
	m.timeline.notice = ""
	m.timelinePresentationCached = false
	m.invalidateRows()
}

func (m *Model) refreshTimelinePresentation() {
	tier := m.activeTimelineTier()
	if m.timelinePresentationCached && m.timelinePresentationTier == tier {
		return
	}
	m.laneOrder = laneOrderFor(m.log, tier)
	m.detailPaneNatural = detailNaturalWidth(m.log, tier)
	m.timelinePresentationTier = tier
	m.timelinePresentationCached = true
}
