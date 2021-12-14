// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"github.com/lf-edge/eve/libs/depgraph"
	"net/http"
	"net/url"
)

// Run local http server that redirects requests to dreampuf.github.io/GraphvizOnline
// for visual rendering of the dependency graph.
func redirectGraphvizRendering(graph depgraph.DepGraph) (redirectAddr string) {
	const (
		targetHost = "http://dreampuf.github.io"
		targetPath = "/GraphvizOnline"
	)
	target, err := url.Parse(targetHost + targetPath)
	if err != nil {
		panic(err)
	}

	redirect := func(w http.ResponseWriter, r *http.Request) {
		dot, err := graph.RenderDOT()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "depgraph rendering to DOT failed: %v\n", err)
			return
		}
		target.Fragment = dot
		// Do not let the client to cache.
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate") // HTTP 1.1.
		w.Header().Set("Pragma", "no-cache") // HTTP 1.0.
		w.Header().Set("Expires", "0") // Proxies.
		http.Redirect(w, r, target.String(), 302)
	}
	http.HandleFunc(targetPath, redirect)

	localSrv := "127.0.0.1:8080"
	go func() {
		err := http.ListenAndServe(localSrv, nil)
		if err != nil {
			panic(err)
		}
	}()

	return "http://" + localSrv + targetPath
}