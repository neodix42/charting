package main

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestBundledVersionUsesCompiledRelease(t *testing.T) {
	assets := fstest.MapFS{"frontend/dist/app-version.json": {Data: []byte(`{"version":"2.3.17","tag":"v2.3.17"}`)}}
	if got := bundledAppVersion(assets); got != "2.3.17" {
		t.Fatalf("compiled release version = %q", got)
	}
}

func TestBundledVersionFallsBackWithoutValidFrontend(t *testing.T) {
	for name, contents := range map[string]string{
		"missing":      "",
		"malformed":    `{`,
		"wrong tag":    `{"version":"2.3.17","tag":"v2.3.16"}`,
		"wrong type":   `{"version":17,"tag":"v17"}`,
		"empty":        `{"version":"","tag":"v"}`,
		"prefixed":     `{"version":"v2.3.17","tag":"vv2.3.17"}`,
		"incomplete":   `{"version":"2.3","tag":"v2.3"}`,
		"leading zero": `{"version":"02.3.17","tag":"v02.3.17"}`,
		"too large":    `{"version":"2.3.65536","tag":"v2.3.65536"}`,
	} {
		t.Run(name, func(t *testing.T) {
			assets := fstest.MapFS{}
			if contents != "" {
				assets["frontend/dist/app-version.json"] = &fstest.MapFile{Data: []byte(contents)}
			}
			if got := bundledAppVersion(assets); got != strings.TrimSpace(sourceVersion) {
				t.Fatalf("fallback version = %q, want VERSION", got)
			}
		})
	}
}
