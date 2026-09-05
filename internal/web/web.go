package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var assets embed.FS

func Handler() http.Handler { sub, _ := fs.Sub(assets, "static"); return http.FileServer(http.FS(sub)) }
