package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type literalPosition struct {
	byteOffset int
	column     int
}

// findLiteral selects from the same non-overlapping occurrences in either
// direction. An anchor excludes itself; without one, column is inclusive.
// A negative column admits the whole line.
func findLiteral(line, query string, forward bool, anchor *literalPosition, column int) (literalPosition, bool) {
	if query == "" {
		return literalPosition{}, false
	}
	var chosen literalPosition
	found := false
	remaining, clusterByte, clusterColumn, state := line, 0, 0, -1
	for offset := 0; offset <= len(line)-len(query); {
		i := strings.Index(line[offset:], query)
		if i < 0 {
			break
		}
		i += offset
		admit := true
		if anchor != nil {
			admit = (forward && i > anchor.byteOffset) || (!forward && i < anchor.byteOffset)
		}
		if admit {
			for clusterByte < i && remaining != "" {
				cluster, rest, width, nextState := ansi.FirstGraphemeCluster(remaining, state)
				if i < clusterByte+len(cluster) {
					break
				}
				clusterByte += len(cluster)
				clusterColumn += width
				remaining, state = rest, nextState
			}
			p := literalPosition{byteOffset: i, column: clusterColumn}
			if anchor == nil && column >= 0 {
				admit = (forward && p.column >= column) || (!forward && p.column <= column)
			}
			if admit {
				chosen, found = p, true
				if forward {
					return chosen, true
				}
			}
		}
		offset = i + len(query)
	}
	return chosen, found
}
