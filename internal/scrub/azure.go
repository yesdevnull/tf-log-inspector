package scrub

import (
	"net/netip"
	"regexp"
	"strings"
)

var azureEndpointPattern = regexp.MustCompile(`(?i)[a-z0-9][a-z0-9-]*\.(?:(?:privatelink\.)?(?:blob|dfs|file|queue|table)\.core\.windows\.net|(?:vault|managedhsm|privatelink\.vaultcore)\.azure\.net)`)

// Only exact service suffixes preserve Azure structure; custom domains use
// ordinary hostname scrubbing. Account and vault labels remain identifying.
func azureService(host string) string {
	_, suffix, ok := strings.Cut(strings.ToLower(strings.TrimSuffix(host, ".")), ".")
	if !ok {
		return ""
	}
	switch suffix {
	case "vault.azure.net", "managedhsm.azure.net", "privatelink.vaultcore.azure.net":
		return "vault"
	}
	suffix = strings.TrimPrefix(suffix, "privatelink.")
	switch suffix {
	case "blob.core.windows.net", "dfs.core.windows.net", "file.core.windows.net", "queue.core.windows.net", "table.core.windows.net":
		return "storage"
	}
	return ""
}

func (s *session) discoverAzureIPRestriction(value string) {
	addresses := strings.Split(value, "-")
	if len(addresses) > 2 {
		return
	}
	parsed := make([]netip.Addr, len(addresses))
	for i, address := range addresses {
		var err error
		parsed[i], err = netip.ParseAddr(address)
		if err != nil {
			return
		}
	}
	var parts []compositePart
	pos := 0
	for i, address := range addresses {
		part := s.part(address, "network", pos, pos+len(address), "")
		part.candidate.ip = parsed[i]
		parts = append(parts, part)
		pos += len(address) + 1
	}
	if len(parts) == 2 {
		s.composite(value, "network", parts, "")
	}
}

func vaultCollection(value string) bool {
	switch strings.ToLower(value) {
	case "secrets", "keys", "certificates":
		return true
	}
	return false
}

func (s *session) discoverAzureEndpoints(v *view) {
	for _, m := range azureEndpointPattern.FindAllStringIndex(v.text, -1) {
		if !boundaries(v.text, m[0], m[1]) || m[0] > 0 && v.text[m[0]-1] == '.' {
			continue
		}
		// A suffix of a longer DNS name is not an Azure service endpoint.
		if m[1]+1 < len(v.text) && v.text[m[1]] == '.' && (word(rune(v.text[m[1]+1])) || v.text[m[1]+1] == '.') {
			continue
		}
		value := v.text[m[0]:m[1]]
		s.discoverHost(value)
		s.markComposite(v, value, m[0])
	}
}
