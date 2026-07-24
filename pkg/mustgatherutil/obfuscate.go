/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package mustgatherutil

import (
	goflag "flag"
	"fmt"
	"os"

	mgclean "github.com/openshift/must-gather-clean/pkg/cli"
	_ "go.uber.org/automaxprocs"
	"k8s.io/klog/v2"
)

const (
	// DefaultObfuscateConfigPath is the built-in obfuscation config location in the operator image.
	DefaultObfuscateConfigPath = "/etc/must-gather-clean/default-config.yaml"
	// ObfuscateWorkerCount is the number of parallel workers for must-gather-clean traversal.
	ObfuscateWorkerCount = 4
)

// RunObfuscate executes the obfuscate subcommand. Args are everything after "obfuscate" on the command line.
// Returns an exit code suitable for os.Exit.
func RunObfuscate(args []string) int {
	fs := goflag.NewFlagSet("obfuscate", goflag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	klog.InitFlags(fs)

	input := fs.String("input", "", "directory containing the must-gather bundle to obfuscate")
	output := fs.String("output", "", "directory where the obfuscated bundle is written")
	config := fs.String("config", DefaultObfuscateConfigPath, "path to the must-gather-clean configuration file")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse obfuscate flags: %v\n", err)
		return 1
	}

	if *input == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "obfuscate requires --input and --output")
		return 1
	}

	if err := mgclean.Run(*config, *input, *output, true, *output, ObfuscateWorkerCount); err != nil {
		fmt.Fprintf(os.Stderr, "obfuscation failed: %v\n", err)
		return 1
	}

	return 0
}
