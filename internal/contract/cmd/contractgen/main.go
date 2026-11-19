// SPDX-License-Identifier: MIT

// Command contractgen turns the contract into Go code. scripts/gen.sh runs it
// after bundling the OpenAPI document. It writes:
//
//   - internal/contract/api: models, the route table and the embedded spec
//   - internal/contract/api/<tag>api: one strict server interface per tag
//   - internal/contract/<name>: types for each JSON Schema
//   - runners/sdk/protocol: runner protocol types and method names
//
// Output is deterministic: the same contract gives the same bytes.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if err := run(*root); err != nil {
		fmt.Fprintln(os.Stderr, "contractgen:", err)
		os.Exit(1)
	}
}

func run(root string) error {
	r := repo{root: root}
	spec, err := loadSpec(r.path("contract/dist/openapi.json"))
	if err != nil {
		return err
	}
	if err := generateAPI(r, spec); err != nil {
		return fmt.Errorf("api: %w", err)
	}
	if err := generateSchemas(r); err != nil {
		return fmt.Errorf("schemas: %w", err)
	}
	if err := generateRunnerMethods(r); err != nil {
		return fmt.Errorf("runner protocol: %w", err)
	}
	return nil
}
