package main

import (
	"os"

	"github.com/celestezj/blog-bootstrap/internal/app"
	"github.com/celestezj/blog-bootstrap/internal/platform"
)

var version = "dev"

func main() {
	platform.EnsureUTF8Console()
	os.Exit(app.Main(version))
}
