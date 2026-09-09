package scrub

import "strings"

func publicProvider(name string) bool {
	switch name {
	case "aws", "azurerm", "azuread", "google", "local", "null", "random", "time", "tls", "github":
		return true
	}
	return false
}

func publicAddress(value string) bool {
	if value == "registry.terraform.io/integrations/github" {
		return true
	}
	if name, ok := strings.CutPrefix(value, "registry.terraform.io/hashicorp/"); ok {
		return name != "github" && publicProvider(name)
	}
	return false
}

func (s *session) provider(v *view, start, end int, component bool) {
	value := v.text[start:end]
	if component {
		if !strings.HasPrefix(value, "provider.") {
			v.protect(start, end)
			return
		}
		nameStart := start + len("provider.")
		if strings.HasPrefix(v.text[nameStart:end], "terraform-provider-") {
			nameStart += len("terraform-provider-")
		}
		nameEnd := end
		if at := strings.Index(v.text[nameStart:end], "_v"); at >= 0 {
			nameEnd = nameStart + at
		}
		if publicProvider(v.text[nameStart:nameEnd]) {
			v.protect(start, end)
			return
		}
		v.protect(start, nameStart)
		v.protect(nameEnd, end)
		s.discover(v.text[nameStart:nameEnd], "name", false)
		v.allowed = append(v.allowed, region{nameStart, nameEnd})
		return
	}
	if value == "provider" || publicAddress(value) {
		v.protect(start, end)
		return
	}
	pos := start
	for index, part := range strings.Split(value, "/") {
		s.discover(part, "name", false)
		v.allowed = append(v.allowed, region{pos, pos + len(part)})
		if index == 0 && part != "" {
			s.candidates[part].hostname = true
		}
		pos += len(part) + 1
	}
}

func (s *session) mappedProvider(value string, component bool) (string, error) {
	v := &view{text: value}
	s.provider(v, 0, len(value), component)
	counts := s.counts
	s.counts = make(map[string]int)
	defer func() { s.counts = counts }()
	return s.render(v)
}
