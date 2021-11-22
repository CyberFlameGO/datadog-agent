package lutgen

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"

	"github.com/go-delve/delve/pkg/goversion"

	"github.com/DataDog/datadog-agent/pkg/network/go/rungo"
	"github.com/DataDog/datadog-agent/pkg/network/go/rungo/matrix"
)

// LookupTableGenerator configures the process of generating
// a Go source file that implements lookup table(s) for any value
// based on an input Go version and Go architecture.
// This can be used to resolve values about a binary that change
// based on the Go version/architecture,
// by compiling and inspecting small test binaries ahead-of-type
// for each Go version/architecture and generating the lookup table(s)
// based on the results of this process.
//
// Each lookup table is implemented as a function,
// which are configured in LookupFunction types.
type LookupTableGenerator struct {
	Package                string
	MinGoVersion           goversion.GoVersion
	MaxQuickGoVersion      *goversion.GoVersion
	Architectures          []string
	CompilationParallelism int
	LookupFunctions        []LookupFunction
	ExtraImports           []string
	InspectBinary          func(binary Binary) (interface{}, error)
	TestProgramPath        string
	InstallDirectory       string
	OutDirectory           string

	allBinaries   []Binary
	allBinariesMu sync.Mutex
}

// Binary wraps the information about a single compiled test binary
// that is given to the inspection callback.
type Binary struct {
	Architecture    string
	GoVersion       goversion.GoVersion
	GoVersionString string
	Path            string
}

type architectureVersion struct {
	architecture string
	version      goversion.GoVersion
}

// Run runs the generator to completion,
// writing the generated Go source code to the given writer.
// If an error occurs installing Go toolchain versions,
// compiling the test program, or running the inspection function
// (or if the context is cancelled),
// then the function will return early.
func (g *LookupTableGenerator) Run(ctx context.Context, writer io.Writer) error {
	versions, err := g.getVersions(ctx)
	if err != nil {
		return err
	}

	// Create a folder to store the compiled binaries
	err = os.MkdirAll(g.OutDirectory, 0o777)
	if err != nil {
		return err
	}

	log.Println("running lookup table generation")
	log.Printf("architectures: %v", g.Architectures)
	sortedVersions := make([]goversion.GoVersion, len(versions))
	copy(sortedVersions, versions)
	sort.Slice(sortedVersions, func(x, y int) bool {
		return !sortedVersions[x].AfterOrEqual(sortedVersions[y])
	})
	log.Println("versions:")
	for _, v := range sortedVersions {
		log.Printf("- %s", versionToString(v))
	}

	// Create a matrix runner to build the test program
	// against each combination of Go version and architecture.
	matrixRunner := &matrix.Runner{
		Parallelism:      g.CompilationParallelism,
		Versions:         versions,
		Architectures:    g.Architectures,
		InstallDirectory: g.InstallDirectory,
		GetCommand:       g.getCommand,
	}
	err = matrixRunner.Run(ctx)
	if err != nil {
		return err
	}

	// For all of the output binaries, run the inspection logic
	resultTable, err := g.inspectAllBinaries(ctx)
	if err != nil {
		return err
	}

	// For each lookup function, prepare the template arguments
	lookupFunctionArgs := []lookupFunctionTemplateArgs{}
	for _, lookupFn := range g.LookupFunctions {
		lookupFunctionArgs = append(lookupFunctionArgs, lookupFn.argsFromResultTable(resultTable))
	}

	// Construct the overall template args struct and render it
	args := templateArgs{
		Imports: append([]string{
			"fmt",
			"github.com/go-delve/delve/pkg/goversion",
		}, g.ExtraImports...),
		Package:                g.Package,
		LookupFunctions:        lookupFunctionArgs,
		MinGoVersion:           g.MinGoVersion,
		SupportedArchitectures: g.Architectures,
	}
	return args.Render(writer)
}

func (g *LookupTableGenerator) getVersions(ctx context.Context) ([]goversion.GoVersion, error) {
	// Download a list of all of the go versions
	allRawVersions, err := rungo.ListGoVersions(ctx)
	if err != nil {
		return nil, err
	}

	// Parse each Go version to the struct,
	// and filter versions to those greater than the minimum,
	// and non-beta or RC versions.
	// Additionally, if g.MaxQuickGoVersion is set,
	// then exclude versions that are not the initial point release
	// for each major, minor version pair
	// up until the max quick Go version.
	// This lets revision-level version bumps
	// be skipped when it is known that
	// they don't cause different lookup table values.
	allVersions := []goversion.GoVersion{}
	for _, rawVersion := range allRawVersions {
		if version, ok := goversion.Parse(fmt.Sprintf("go%s", rawVersion)); ok {
			if version.Beta != 0 ||
				version.RC != 0 ||
				version.Proposal != "" ||
				!version.AfterOrEqual(g.MinGoVersion) {
				continue
			}

			// Check if the version's major, minor pair falls under the set
			// of "quick" Go versions where non-zero revisions are skipped.
			if g.MaxQuickGoVersion != nil &&
				g.MaxQuickGoVersion.AfterOrEqual(version) &&
				version.Rev > 0 {
				continue
			}

			allVersions = append(allVersions, version)
		}
	}

	return allVersions, nil
}

func (g *LookupTableGenerator) addBinary(binary Binary) {
	g.allBinariesMu.Lock()
	defer g.allBinariesMu.Unlock()

	g.allBinaries = append(g.allBinaries, binary)
}

func (g *LookupTableGenerator) getAllBinaries() []Binary {
	g.allBinariesMu.Lock()
	defer g.allBinariesMu.Unlock()

	newSlice := make([]Binary, len(g.allBinaries))
	copy(newSlice, g.allBinaries)
	return newSlice
}

func (g *LookupTableGenerator) getCommand(ctx context.Context, version goversion.GoVersion, arch string) *exec.Cmd {
	versionStr := versionToString(version)
	outPath := filepath.Join(g.OutDirectory, fmt.Sprintf("%s.go%s", arch, versionStr))

	modFile, err := g.createFakeGoMod(version, arch)
	if err != nil {
		log.Printf("error creating go.mod file for (Go version, arch pair) (go%s, %s): %v", versionStr, arch, err)
		return nil
	}

	// Store the binary struct in a list so that it can later be opened.
	// If the command ends up failing, this will be ignored
	// and the entire lookup table generation will exit early.
	g.addBinary(Binary{
		Path:            outPath,
		GoVersion:       version,
		GoVersionString: versionStr,
		Architecture:    arch,
	})

	command := exec.CommandContext(
		ctx,
		"go",
		"build",
		"-o", outPath,
		"-modfile", modFile,
		"--",
		g.TestProgramPath,
	)

	// Set the GOPATH and GOCACHE variables.
	// Make sure to resolve the absolute path of install directory first.
	installDirectoryAbs, err := filepath.Abs(g.InstallDirectory)
	if err != nil {
		log.Printf("error install directory at %q: %v", g.InstallDirectory, err)
		return nil
	}
	command.Env = append(command.Env, fmt.Sprintf("%s=%s", "GOPATH", filepath.Join(installDirectoryAbs, "build-gopath")))
	command.Env = append(command.Env, fmt.Sprintf("%s=%s", "GOCACHE", filepath.Join(installDirectoryAbs, "build-gocache")))

	return command
}

// createFakeGoMod creates the go.mod file that the `go build` command should use
// when compiling the test program.
// This is needed to prevent the test program compilation from using the
// `go.mod` file in the root of this repository.
func (g *LookupTableGenerator) createFakeGoMod(version goversion.GoVersion, arch string) (string, error) {
	path := filepath.Join(g.OutDirectory, fmt.Sprintf("%s.go%s.go.mod", arch, versionToString(version)))

	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("error creating file at %q: %w", path, err)
	}
	defer f.Close()

	_, err = f.WriteString(fmt.Sprintf("module test_program\n\ngo %d.%d\n", version.Major, version.Minor))
	if err != nil {
		return "", fmt.Errorf("error writing contents: %w", err)
	}

	return path, nil
}

// inspectAllBinaries runs the inspection function for each binary in parallel,
// returning a "result table" that maps architecture,version pairs
// to the result of the inspection.
func (g *LookupTableGenerator) inspectAllBinaries(ctx context.Context) (map[architectureVersion]interface{}, error) {
	// Get all of the binaries that were generated from the matrix runner
	binaries := g.getAllBinaries()

	results := make(chan struct {
		bin    Binary
		result interface{}
		err    error
	})
	for _, bin := range binaries {
		go func(bin Binary) {
			result, err := g.InspectBinary(bin)
			results <- struct {
				bin    Binary
				result interface{}
				err    error
			}{bin, result, err}
		}(bin)
	}

	resultTable := make(map[architectureVersion]interface{})
	for range binaries {
		select {
		case result := <-results:
			if result.err != nil {
				// Bail early and return
				return nil, result.err
			}

			resultTable[architectureVersion{result.bin.Architecture, result.bin.GoVersion}] = result.result
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return resultTable, nil
}

func versionToString(v goversion.GoVersion) string {
	if v.Rev == 0 {
		return fmt.Sprintf("%d.%d", v.Major, v.Minor)
	}

	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Rev)
}
