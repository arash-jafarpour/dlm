package main

import (
	"fmt"

	"modules/downloader"
	"modules/reader"
)

func main() {
	links, err := reader.ReadLinks("links.txt")
	if err != nil {
		panic(fmt.Sprintf("error reading links: %v", err))
	}

	d := downloader.New("./downloads")

	for _, url := range links.Links {
		fmt.Printf("→ downloading: %s\n", url)
		if err := d.Download(url); err != nil {
			fmt.Printf("✗ failed: %v\n", err)
		}
	}
}
