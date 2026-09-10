package model

import "testing"

func TestResourceModuleBoundaries(t *testing.T) {
	cases := []struct {
		parent, child string
		want          bool
	}{
		{"", `module.app[0]`, true},
		{`module.app`, `module.app.module.db`, true},
		{`module.app`, `module.application`, false},
		{`module.app`, `module.app[0]`, false},
		{`module.app[0]`, `module.app[1]`, false},
		{`module.app["a.b"]`, `module.app["a.b"].module.db`, true},
		{`module.app["a"]`, `module.app["a.b"]`, false},
	}
	for _, tc := range cases {
		if got := ModuleContains(tc.parent, tc.child); got != tc.want {
			t.Errorf("%q contains %q = %v", tc.parent, tc.child, got)
		}
	}
	got := ResolveResourceModule(`module.m["k"].aws_instance.web[0]`, "", false)
	if !got.Known || got.Path != `module.m["k"]` {
		t.Fatalf("module=%+v", got)
	}
	if ResolveResourceModule(`module.m.aws_instance.web`, "", true).Known {
		t.Fatal("conflicting observed root accepted")
	}
}

func TestResourceModuleRecognisesCompleteResourceShapes(t *testing.T) {
	cases := []struct{ address, want string }{
		{"aws_instance.web", ""},
		{"data.local_file.config", ""},
		{"ephemeral.random_password.token", ""},
		{`module.应用.module.db[0].aws_instance.web["x"]`, `module.应用.module.db[0]`},
		{`module.m["a\\\"b.c\\]d"].data.local_file.config`, `module.m["a\\\"b.c\\]d"]`},
	}
	for _, tc := range cases {
		got := ResolveResourceModule(tc.address, "", false)
		if !got.Known || got.Path != tc.want {
			t.Errorf("ResolveResourceModule(%q) = %+v, want known %q", tc.address, got, tc.want)
		}
	}
}

func TestResourceModuleRejectsIncompleteOrUnsupportedAddresses(t *testing.T) {
	for _, address := range []string{
		`module.m["open].aws_instance.web`, `module.m.`, `module.m`,
		`aws_instance`, `data.local_file`, `ephemeral.random_password`,
		`module.m[0][1].aws_instance.web`, "aws_instance.we\nb",
		`module.bad name.aws_instance.web`, `module.9name.aws_instance.web`,
		`module.bad+name.aws_instance.web`, `module.m.aws_instance.web!`,
	} {
		if got := ResolveResourceModule(address, "", false); got.Known {
			t.Errorf("ResolveResourceModule(%q) = %+v, want unknown", address, got)
		}
	}
}

func TestResourceModuleAcceptsConservativeTerraformIdentifiers(t *testing.T) {
	for _, address := range []string{
		`module._private.aws_instance.web`,
		`module.app-name.aws_instance.web_2`,
		`module.应用.aws_实例.网页2`,
		`module.℘module.aws_instance.web`,
		`module.Ⅰmodule.aws_instance.web`,
		`module.a·b.aws_instance.web`,
	} {
		if got := ResolveResourceModule(address, "", false); !got.Known {
			t.Errorf("ResolveResourceModule(%q) = %+v, want known", address, got)
		}
	}
}

func TestResourceModuleAcceptsOpeningBracketInsideQuotedKey(t *testing.T) {
	const address = `module.m["a[b"].aws_instance.web`
	got := ResolveResourceModule(address, "", false)
	if !got.Known || got.Path != `module.m["a[b"]` {
		t.Errorf("ResolveResourceModule(%q) = %+v, want quoted bracket retained", address, got)
	}
}

func TestResourceModuleObservedEvidenceWinsOnlyWhenValidAndConsistent(t *testing.T) {
	cases := []struct {
		name, address, observed string
		known                   bool
		want                    ResourceModule
	}{
		{"matching", `module.m[0].aws_instance.web`, `module.m[0]`, true, ResourceModule{`module.m[0]`, true}},
		{"valid observed with unknown address", `unsupported`, `module.m`, true, ResourceModule{`module.m`, true}},
		{"malformed observed", `module.m.aws_instance.web`, `module.`, true, ResourceModule{}},
		{"conflict", `module.m.aws_instance.web`, `module.n`, true, ResourceModule{}},
		{"explicit root conflict", `module.m.aws_instance.web`, "", true, ResourceModule{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveResourceModule(tc.address, tc.observed, tc.known); got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestModuleContainsRejectsMalformedModulePaths(t *testing.T) {
	for _, tc := range [][2]string{{`module.`, `module.m`}, {`module.m`, `module.m.`}, {`module.m[0][1]`, `module.m[0][1]`}} {
		if ModuleContains(tc[0], tc[1]) {
			t.Errorf("ModuleContains(%q, %q) = true, want false", tc[0], tc[1])
		}
	}
}
