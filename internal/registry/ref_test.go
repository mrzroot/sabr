package registry

import "testing"

func TestParseRef(t *testing.T) {
	cases := []struct{ in, reg, repo, tag, digest, name string }{
		{"alpine", "registry-1.docker.io", "library/alpine", "latest", "", "alpine:latest"},
		{"alpine:3.20", "registry-1.docker.io", "library/alpine", "3.20", "", "alpine:3.20"},
		{"docker.io/library/nginx:1", "registry-1.docker.io", "library/nginx", "1", "", "nginx:1"},
		{"user/app", "registry-1.docker.io", "user/app", "latest", "", "user/app:latest"},
		{"ghcr.io/o/r:v1", "ghcr.io", "o/r", "v1", "", "ghcr.io/o/r:v1"},
		{"localhost:5000/x", "localhost:5000", "x", "latest", "", "localhost:5000/x:latest"},
		{"alpine@sha256:abc", "registry-1.docker.io", "library/alpine", "", "sha256:abc", "alpine"},
		{"docker.arvancloud.ir/redis:7", "docker.arvancloud.ir", "redis", "7", "", "docker.arvancloud.ir/redis:7"},
	}
	for _, c := range cases {
		r, err := ParseRef(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if r.Registry != c.reg || r.Repo != c.repo || r.Tag != c.tag || r.Digest != c.digest || r.Name() != c.name {
			t.Errorf("%s: got %+v name %s", c.in, r, r.Name())
		}
	}
	for _, bad := range []string{"", "a b", "UPPER/x", "x@md5:1"} {
		if _, err := ParseRef(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestPickPlatform(t *testing.T) {
	mk := func(os, arch, v string) descriptor {
		d := descriptor{Digest: os + arch + v}
		d.Platform = &struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
			Variant      string `json:"variant,omitempty"`
		}{os, arch, v}
		return d
	}
	ms := []descriptor{mk("linux", "amd64", ""), mk("linux", "arm", "v7"), mk("linux", "arm64", "v8"), mk("unknown", "unknown", "")}
	if d, err := pickPlatform(ms, "linux/arm64"); err != nil || d.Digest != "linuxarm64v8" {
		t.Fatalf("arm64: %v %v", d, err)
	}
	if d, err := pickPlatform(ms, "linux/arm/v7"); err != nil || d.Digest != "linuxarmv7" {
		t.Fatalf("arm/v7: %v %v", d, err)
	}
	if _, err := pickPlatform(ms, "windows/amd64"); err == nil {
		t.Fatal("expected no match")
	}
}
