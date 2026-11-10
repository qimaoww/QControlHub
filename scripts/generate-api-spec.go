//go:build ignore

package main

import (
	"bytes"
	"fmt"
	"github.com/qimaoww/qcontrolhub/internal/apidoc"
	"os"
)

func main() {
	data, err := apidoc.Generate(".")
	if err != nil {
		panic(err)
	}
	path := "internal/api/contract/openapi.json"
	if len(os.Args) > 1 && os.Args[1] == "--check" {
		old, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(old, data) {
			panic("API contract drift: run go run scripts/generate-api-spec.go")
		}
		fmt.Println("API contract matches routes, models and key policies")
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		panic(err)
	}
}
