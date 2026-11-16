package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/extension/install"
	"github.com/pulseaiclub/phi/internal/extension/loader"
)

func TestPluginListAligned(t *testing.T) {
	installed := []install.Installed{
		{
			ID:       "greet",
			Path:     "/home/u/.phi/extensions/greet",
			Manifest: loader.Manifest{Version: "0.1.0"},
			Meta:     install.InstallMeta{Source: "release", ReleaseTag: "v1.2.3"},
			Managed:  true,
		},
		{
			ID:       "local-tool",
			Path:     "/home/u/.phi/extensions/local-tool",
			Manifest: loader.Manifest{Version: "0.0.1"},
		},
		{
			ID:      "x",
			Path:    "/p/x",
			Meta:    install.InstallMeta{Source: "git"},
			Managed: true,
		},
	}
	var out strings.Builder
	writePluginList(&out, installed)

	want := "ID          VERSION  SOURCE   PATH\n" +
		"greet       v1.2.3   release  /home/u/.phi/extensions/greet\n" +
		"local-tool  0.0.1    manual   /home/u/.phi/extensions/local-tool\n" +
		"x                    git      /p/x\n"
	require.Equal(t, want, out.String())
}

func TestPluginListEmpty(t *testing.T) {
	var out strings.Builder
	writePluginList(&out, nil)

	require.Equal(t, "(no plugins — try: phi plugin install alice/greet)\n", out.String())
}
