// Copyright (c) 2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

// A simple demonstration of depgraph.
// Files, directories and their dependencies are represented using the dependency
// graph, which then takes care of the synchronization between the intended and the
// actual content of a (temporary) directory.

package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/lf-edge/eve/libs/depgraph"
)

const (
	colorReset = "\033[0m"
	colorRed = "\033[31m"
	colorGreen = "\033[32m"
	colorCyan = "\033[36m"
)

type logger struct{}

// Noticef : log debug message.
func (l logger) Noticef(format string, args ...interface{}) {
	fmt.Printf(colorCyan + "[DEBUG] " + format + "\n" + colorReset, args...)
}

// Errorf : log error message.
func (l logger) Errorf(format string, args ...interface{}) {
	fmt.Printf(colorRed + "[ERROR] " + format + "\n" + colorReset, args...)
}

func printReport(format string, args ...interface{}) {
	fmt.Printf(colorGreen + format + "\n" + colorReset, args...)
}

func svgImageCircles(numOfCircles int) string {
	image := "<svg height=\"300\" width=\"200\">\n"
	for i:=0; i < numOfCircles; i++ {
		image += fmt.Sprintf("<circle cx=\"50\" cy=\"%d\" r=\"20\" fill=\"red\"/>\n",
			50+i*50)
	}
	image += "</svg>"
	return image
}

func svgImageSquares(numOfSquares int) string {
	image := "<svg height=\"300\" width=\"200\">\n"
	for i:=0; i < numOfSquares; i++ {
		image += fmt.Sprintf("<rect width=\"50\" height=\"50\" x=\"50\" y=\"%d\" fill=\"blue\"/>\n",
			50+i*50)
	}
	image += "</svg>"
	return image
}

func shellScript(cmds string) string {
	return fmt.Sprintf("#!/bin/sh\n%s", cmds)
}

func main() {
	// Initialize the graph and register configurators for files and directories.
	ctx := context.Background()
	log := logger{}
	g := depgraph.NewDepGraph(log)
	graphvizAddr := redirectGraphvizRendering(g)
	err := g.RegisterConfigurator(fileConfigurator{}, file{}.Type())
	if err != nil {
		log.Errorf("Failed to register configurator for files: %v", err)
		os.Exit(1)
	}
	err = g.RegisterConfigurator(dirConfigurator{}, directory{}.Type())
	if err != nil {
		log.Errorf("Failed to register configurator for directories: %v", err)
		os.Exit(1)
	}

	// Create root directory for our file-sync demo.
	dir, err := ioutil.TempDir("/tmp", "file-sync-demo-")
	if err != nil {
		log.Errorf("Failed to create root directory for the demo: %v", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	// Initial intended state of the directory content.
	// We want directory with svg images, further sorted between sub-directories.
	// The whole svg-image directory and all its content will be represented by a depgraph *cluster*.
	// Then we want directory with shell scripts and another empty directory later used for text files.
	description := fmt.Sprintf(`%s
├── svg-images (this directory and all its content is represented by a depgraph cluster)
│   ├── circles
│   │   ├── one-circle.svg
│   │   └── two-circles.svg
│   └── squares
│       └── one-square.svg
├── scripts
│   ├── hello-world.sh
│   └── ls.sh
└── text-files (empty dir)
`, dir)
	rootDir := directory{dirname: dir,	permissions: 0755}
	svgImagesDir := directory{dirname: "svg-images", parent: &rootDir, permissions: 0755}
	circlesDir := directory{dirname: "circles", parent: &svgImagesDir, permissions: 0755}
	squaresDir := directory{dirname: "squares", parent: &svgImagesDir, permissions: 0755}
	scriptsDir := directory{dirname: "scripts", parent: &rootDir, permissions: 0755}
	textFilesDir := directory{dirname: "text-files", parent: &rootDir, permissions: 0755}

	oneCircleFile := file{id: newFileID(), filename: "one-circle.svg",
		content: svgImageCircles(1), permissions: 0644, parentDir: &circlesDir}
	twoCircleFile := file{id: newFileID(), filename: "two-circles.svg",
		content: svgImageCircles(2), permissions: 0644, parentDir: &circlesDir}
	oneSquareFile := file{id: newFileID(), filename: "one-square.svg",
		content: svgImageSquares(1), permissions: 0644, parentDir: &squaresDir}
	helloWorldFile := file{id: newFileID(), filename: "hello-world.sh",
		content: shellScript("echo 'Hello world!'"), permissions: 0744, parentDir: &scriptsDir}
	lsFile := file{id: newFileID(), filename: "ls.sh",
		content: shellScript("ls -al"), permissions: 0744, parentDir: &scriptsDir}

	svgImages := depgraph.Cluster{
		Name:        "svg-images",
		Description: "All SVG images",
		Items:       []depgraph.Item{
			svgImagesDir,
			circlesDir,
			squaresDir,
			oneCircleFile,
			twoCircleFile,
			oneSquareFile,
		},
	}

	// Update the graph content and synchronize the actual and the intended state.
	g.Item(rootDir.Name()).Put(rootDir)
	g.Cluster(svgImages.Name).Put(svgImages)
	g.Item(scriptsDir.Name()).Put(scriptsDir)
	g.Item(helloWorldFile.Name()).Put(helloWorldFile)
	g.Item(lsFile.Name()).Put(lsFile)
	g.Item(textFilesDir.Name()).Put(textFilesDir)
	err = g.Sync(ctx)
	if err != nil {
		log.Errorf("depgraph sync failed: %v", err)
		os.Exit(1)
	}

	// Inform the user.
	printReport("Applied the intended state:")
	fmt.Println(description)
	printReport("depgraph DOT rendering: %s ", graphvizAddr)
	printReport("Verify the content of %s and press ENTER to continue", dir)
	_, _ = fmt.Scanln()

	// Next intended state of the directory content.
	// Now we want all svg images to be directly under svg-images.
	// Script ls.sh should no longer exist. Script hello-world.sh has modified content.
	// Directory with text files should now contain two files.
	// depgraph will perform create/modify/delete operations to get from the current state
	// to the new intended state.
	description = fmt.Sprintf(`%s
├── svg-images
│   ├── one-circle.svg (moved)
│   ├── two-circles.svg (moved)
│   └── one-square.svg (moved)
├── scripts
│   └── hello-world.sh (modified to German language)
└── text-files
    ├── empty-file.txt (new)
    └── sample-file.txt (new)
`, dir)
	oneCircleFile.parentDir = &svgImagesDir
	twoCircleFile.parentDir = &svgImagesDir
	oneSquareFile.parentDir = &svgImagesDir

	helloWorldFile.content = shellScript("echo 'Hallo Welt!'")
	emptyFile := file{id: newFileID(), filename: "empty-file.txt",
		content: "", permissions: 0644, parentDir: &textFilesDir}
	sampleFile := file{id: newFileID(), filename: "sample-file.txt",
		content: "sample", permissions: 0644, parentDir: &textFilesDir}

	svgImages = depgraph.Cluster{
		Name:        "svg-images",
		Description: "All SVG images",
		Items:       []depgraph.Item{
			svgImagesDir,
			oneCircleFile,
			twoCircleFile,
			oneSquareFile,
		},
	}

	// Update the graph content and synchronize the actual and the intended state.
	g.Cluster(svgImages.Name).Put(svgImages) // replace the cluster content
	g.Item(helloWorldFile.Name()).Put(helloWorldFile) // modify the file content
	g.Item(lsFile.Name()).Del()
	g.Item(emptyFile.Name()).Put(emptyFile)
	g.Item(sampleFile.Name()).Put(sampleFile)
	err = g.Sync(ctx)
	if err != nil {
		log.Errorf("depgraph sync failed: %v", err)
		os.Exit(1)
	}

	// Inform the user.
	printReport("Applied the intended state:")
	fmt.Println(description)
	printReport("depgraph DOT rendering: %s", graphvizAddr)
	printReport("Verify the content of %s and press ENTER to continue", dir)
	_, _ = fmt.Scanln()

	// Finally, remove the root from the graph. Since everything either directly
	// or transitively depends on it, all files and directories will be removed
	// and marked as pending (ItemStatePending).
	g.Item(rootDir.Name()).Del()
	err = g.Sync(ctx)
	if err != nil {
		log.Errorf("depgraph sync failed: %v", err)
		os.Exit(1)
	}

	// Inform the user.
	printReport("Removed root from the graph.")
	printReport("All files and directories under %s should be removed "+
		"and in the pending state.", dir)
	printReport("depgraph DOT rendering: %s", graphvizAddr)
	printReport("Verify the content of %s and press ENTER to exit", dir)
	_, _ = fmt.Scanln()
}
