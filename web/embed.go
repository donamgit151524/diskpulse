package web

import "embed"

// StaticFS embeds the local dashboard HTML, CSS, and JS assets into the binary.
//
//go:embed static/*
var StaticFS embed.FS
