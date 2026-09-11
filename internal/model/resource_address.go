package model

import (
	"github.com/yesdevnull/tf-log-inspector/internal/resourceaddr"
)

// ResourceModule identifies a structurally recognised Terraform module path.
// The empty path is the known root module; Known false means the available
// evidence could not establish membership safely.
type ResourceModule struct {
	Path  string
	Known bool
}

// ResolveResourceModule prefers valid observed metadata and otherwise derives
// module identity from a complete, structurally recognised resource address.
func ResolveResourceModule(address, observed string, observedKnown bool) ResourceModule {
	addressModule := resourceAddressModule(address)
	if !observedKnown {
		return addressModule
	}
	if _, ok := resourceaddr.ModuleSegments(observed); !ok {
		return ResourceModule{}
	}
	if addressModule.Known && addressModule.Path != observed {
		return ResourceModule{}
	}
	return ResourceModule{Path: observed, Known: true}
}

// ModuleContains reports whether child is parent or one of its descendants.
// Both inputs must be complete, structurally recognised module paths.
func ModuleContains(parent, child string) bool {
	parentSegments, parentOK := resourceaddr.ModuleSegments(parent)
	childSegments, childOK := resourceaddr.ModuleSegments(child)
	if !parentOK || !childOK || len(parentSegments) > len(childSegments) {
		return false
	}
	for i := range parentSegments {
		if parentSegments[i] != childSegments[i] {
			return false
		}
	}
	return true
}

func resourceAddressModule(address string) ResourceModule {
	parsed, ok := resourceaddr.Parse(address)
	return ResourceModule{Path: parsed.Module, Known: ok}
}

func moduleSegments(path string) ([]string, bool) { return resourceaddr.ModuleSegments(path) }
