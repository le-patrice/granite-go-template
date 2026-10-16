package main

import (
	"flag"
	"fmt"
	"os"

	"example.com/app/internal/app"
	"example.com/app/internal/config"
)

func main() {
	version := flag.String("version", "3.1", "OpenAPI version to output (3.0 or 3.1)")
	outPath := flag.String("o", "", "Output file path (default: stdout)")
	flag.Parse()

	serviceName := os.Getenv("SERVICE_NAME")
	if serviceName == "" {
		serviceName = "{{APP_NAME}}"
	}
	appVersion := os.Getenv("APP_VERSION")
	if appVersion == "" {
		appVersion = "0.1.0"
	}

	cfg := config.Config{
		ServiceName: serviceName,
		AppVersion:  appVersion,
	}

	spec, err := app.BuildOpenAPI(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error generating openapi spec: %v\n", err)
		os.Exit(1)
	}

	var data []byte
	switch *version {
	case "3.0", "3.0.3":
		data, err = spec.DowngradeYAML()
	case "3.1", "3.1.0":
		data, err = spec.YAML()
	default:
		fmt.Fprintf(os.Stderr, "unsupported openapi version %q (expected 3.0 or 3.1)\n", *version)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error serializing openapi yaml: %v\n", err)
		os.Exit(1)
	}

	if *outPath == "" || *outPath == "-" {
		_, _ = os.Stdout.Write(data)
		return
	}

	if err := os.WriteFile(*outPath, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing to %s: %v\n", *outPath, err)
		os.Exit(1)
	}
}
