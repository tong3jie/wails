package build

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/samber/lo"
	"github.com/wailsapp/wails/v2/internal/colour"
	"github.com/wailsapp/wails/v2/internal/staticanalysis"
	"github.com/wailsapp/wails/v2/pkg/commands/bindings"

	"github.com/wailsapp/wails/v2/internal/fs"

	"github.com/wailsapp/wails/v2/internal/shell"

	"github.com/wailsapp/wails/v2/internal/project"
	"github.com/wailsapp/wails/v2/pkg/clilogger"
)

// Mode is the type used to indicate the build modes
type Mode int

const (
	// Dev mode
	Dev Mode = iota
	// Production mode
	Production
	// Debug build
	Debug
)

// Options contains all the build options as well as the project data
type Options struct {
	LDFlags           string               // Optional flags to pass to linker
	UserTags          []string             // Tags to pass to the Go compiler
	Logger            *clilogger.CLILogger // All output to the logger
	OutputType        string               // EG: desktop, server....
	Mode              Mode                 // release or dev
	ProjectData       *project.Project     // The project data
	Pack              bool                 // Create a package for the app after building
	Platform          string               // The platform to build for
	Arch              string               // The architecture to build for
	Compiler          string               // The compiler command to use
	SkipModTidy       bool                 //  Skip mod tidy before compile
	IgnoreFrontend    bool                 // Indicates if the frontend does not need building
	IgnoreApplication bool                 // Indicates if the application does not need building
	OutputFile        string               // Override the output filename
	BinDirectory      string               // Directory to use to write the built applications
	CleanBinDirectory bool                 // Indicates if the bin output directory should be cleaned before building
	CompiledBinary    string               // Fully qualified path to the compiled binary
	KeepAssets        bool                 // Keep the generated assets/files
	Verbosity         int                  // Verbosity level (0 - silent, 1 - default, 2 - verbose)
	Compress          bool                 // Compress the final binary
	CompressFlags     string               // Flags to pass to UPX
	WebView2Strategy  string               // WebView2 installer strategy
	RunDelve          bool                 // Indicates if we should run delve after the build
	WailsJSDir        string               // Directory to generate the wailsjs module
	ForceBuild        bool                 // Force
	BundleName        string               // Bundlename for Mac
	TrimPath          bool                 // Use Go's trimpath compiler flag
	RaceDetector      bool                 // Build with Go's race detector
	WindowsConsole    bool                 // Indicates that the windows console should be kept
	Obfuscated        bool                 // Indicates that bound methods should be obfuscated
	GarbleArgs        string               // The arguments for Garble
	SkipBindings      bool                 // Skip binding generation
}

// Build the project!
func Build(options *Options) (string, error) {

	// Extract logger
	outputLogger := options.Logger

	// Get working directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// wails js dir
	options.WailsJSDir = options.ProjectData.GetWailsJSDir()

	// Set build directory
	options.BinDirectory = filepath.Join(options.ProjectData.GetBuildDir(), "bin")

	// Save the project type
	options.ProjectData.OutputType = options.OutputType

	// Create builder
	var builder Builder

	switch options.OutputType {
	case "desktop":
		builder = newDesktopBuilder(options)
	case "dev":
		builder = newDesktopBuilder(options)
	default:
		return "", fmt.Errorf("cannot build assets for output type %s", options.ProjectData.OutputType)
	}

	// Set up our clean up method
	defer builder.CleanUp()

	// Initialise Builder
	builder.SetProjectData(options.ProjectData)

	hookArgs := map[string]string{
		"${platform}": options.Platform + "/" + options.Arch,
	}

	for _, hook := range []string{options.Platform + "/" + options.Arch, options.Platform + "/*", "*/*"} {
		if err := execPreBuildHook(outputLogger, options, hook, hookArgs); err != nil {
			return "", err
		}
	}

	// Create embed directories if they don't exist
	if err := CreateEmbedDirectories(cwd, options); err != nil {
		return "", err
	}

	// Generate bindings
	if !options.SkipBindings {
		err = GenerateBindings(options)
		if err != nil {
			return "", err
		}
	}

	if !options.IgnoreFrontend {
		err = builder.BuildFrontend(outputLogger)
		if err != nil {
			return "", err
		}
	}

	compileBinary := ""
	if !options.IgnoreApplication {
		compileBinary, err = execBuildApplication(builder, options)
		if err != nil {
			return "", err
		}
	}

	hookArgs["${bin}"] = compileBinary
	for _, hook := range []string{options.Platform + "/" + options.Arch, options.Platform + "/*", "*/*"} {
		if err := execPostBuildHook(outputLogger, options, hook, hookArgs); err != nil {
			return "", err
		}
	}

	return compileBinary, nil
}

func CreateEmbedDirectories(cwd string, buildOptions *Options) error {
	path := cwd
	if buildOptions.ProjectData != nil {
		path = buildOptions.ProjectData.Path
	}
	embedDetails, err := staticanalysis.GetEmbedDetails(path)
	if err != nil {
		return err
	}

	for _, embedDetail := range embedDetails {
		fullPath := embedDetail.GetFullPath()
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			err := os.MkdirAll(fullPath, 0755)
			if err != nil {
				return err
			}
			f, err := os.Create(filepath.Join(fullPath, "gitkeep"))
			if err != nil {
				return err
			}
			_ = f.Close()
		}
	}

	return nil

}

func GenerateBindings(buildOptions *Options) error {

	obfuscated := buildOptions.Obfuscated
	if obfuscated {
		buildOptions.Logger.Print("  - Generating obfuscated bindings: ")
		buildOptions.UserTags = append(buildOptions.UserTags, "obfuscated")
	} else {
		buildOptions.Logger.Print("  - Generating bindings: ")
	}

	// Generate Bindings
	output, err := bindings.GenerateBindings(bindings.Options{
		Tags:      buildOptions.UserTags,
		GoModTidy: !buildOptions.SkipModTidy,
	})
	if err != nil {
		return err
	}

	if buildOptions.Verbosity == VERBOSE {
		buildOptions.Logger.Println(output)
	}

	buildOptions.Logger.Println("Done.")

	return nil
}

func execBuildApplication(builder Builder, options *Options) (string, error) {
	// Extract logger
	outputLogger := options.Logger

	// If we are building for windows, we will need to generate the asset bundle before
	// compilation. This will be a .syso file in the project root
	if options.Pack && options.Platform == "windows" {
		outputLogger.Print("  - Generating bundle assets: ")
		err := packageApplicationForWindows(options)
		if err != nil {
			return "", err
		}
		outputLogger.Println("Done.")

		// When we finish, we will want to remove the syso file
		defer func() {
			err := os.Remove(filepath.Join(options.ProjectData.Path, options.ProjectData.Name+"-res.syso"))
			if err != nil {
				log.Fatal(err)
			}
		}()
	}

	// Compile the application
	outputLogger.Print("  - Compiling application: ")

	if options.Platform == "darwin" && options.Arch == "universal" {
		outputFile := builder.OutputFilename(options)
		amd64Filename := outputFile + "-amd64"
		arm64Filename := outputFile + "-arm64"

		// Build amd64 first
		options.Arch = "amd64"
		options.OutputFile = amd64Filename
		options.CleanBinDirectory = false
		if options.Verbosity == VERBOSE {
			outputLogger.Println("\nBuilding AMD64 Target: %s", filepath.Join(options.BinDirectory, options.OutputFile))
		}
		err := builder.CompileProject(options)
		if err != nil {
			return "", err
		}
		// Build arm64
		options.Arch = "arm64"
		options.OutputFile = arm64Filename
		options.CleanBinDirectory = false
		if options.Verbosity == VERBOSE {
			outputLogger.Println("Building ARM64 Target: %s", filepath.Join(options.BinDirectory, options.OutputFile))
		}
		err = builder.CompileProject(options)

		if err != nil {
			return "", err
		}
		// Run lipo
		if options.Verbosity == VERBOSE {
			outputLogger.Println("  Running lipo: lipo -create -output %s %s %s", outputFile, amd64Filename, arm64Filename)
		}
		_, stderr, err := shell.RunCommand(options.BinDirectory, "lipo", "-create", "-output", outputFile, amd64Filename, arm64Filename)
		if err != nil {
			return "", fmt.Errorf("%s - %s", err.Error(), stderr)
		}
		// Remove temp binaries
		err = fs.DeleteFile(filepath.Join(options.BinDirectory, amd64Filename))
		if err != nil {
			return "", err
		}
		err = fs.DeleteFile(filepath.Join(options.BinDirectory, arm64Filename))
		if err != nil {
			return "", err
		}
		options.ProjectData.OutputFilename = outputFile
		options.CompiledBinary = filepath.Join(options.BinDirectory, outputFile)
	} else {
		err := builder.CompileProject(options)
		if err != nil {
			return "", err
		}
	}

	outputLogger.Println("Done.")

	// Do we need to pack the app for non-windows?
	if options.Pack && options.Platform != "windows" {

		outputLogger.Print("  - Packaging application: ")

		// TODO: Allow cross platform build
		err := packageProject(options, runtime.GOOS)
		if err != nil {
			return "", err
		}
		outputLogger.Println("Done.")
	}

	if options.Platform == "windows" {
		const expWebView2Loader = "exp_gowebview2loader"

		message := ""
		tags := options.UserTags
		if lo.Contains(tags, expWebView2Loader) {
			message = "Thanks for testing the new experimental Go native WebView2Loader. Please report your feedback and any bugs you think might be related to using the new loader: https://github.com/wailsapp/wails/issues/2004"
		} else {
			tags = append(tags, expWebView2Loader)
			message = fmt.Sprintf("An experimental Go native WebView2Loader is available. We would love to hear your feedback about it and invite you to test it by building with `-tags %s`", strings.Join(tags, ","))
		}
		println(colour.Green("  - " + message))
	}

	return options.CompiledBinary, nil
}

func execPreBuildHook(outputLogger *clilogger.CLILogger, options *Options, hookIdentifier string, argReplacements map[string]string) error {
	preBuildHook := options.ProjectData.PreBuildHooks[hookIdentifier]
	if preBuildHook == "" {
		return nil
	}

	return executeBuildHook(outputLogger, options, hookIdentifier, argReplacements, preBuildHook, "pre")
}

func execPostBuildHook(outputLogger *clilogger.CLILogger, options *Options, hookIdentifier string, argReplacements map[string]string) error {
	postBuildHook := options.ProjectData.PostBuildHooks[hookIdentifier]
	if postBuildHook == "" {
		return nil
	}

	return executeBuildHook(outputLogger, options, hookIdentifier, argReplacements, postBuildHook, "post")

}

func executeBuildHook(outputLogger *clilogger.CLILogger, options *Options, hookIdentifier string, argReplacements map[string]string, buildHook string, hookName string) error {
	if !options.ProjectData.RunNonNativeBuildHooks {
		if hookIdentifier == "" {
			// That's the global hook
		} else {
			platformOfHook := strings.Split(hookIdentifier, "/")[0]
			if platformOfHook == "*" {
				// That's OK, we don't have a specific platform of the hook
			} else if platformOfHook == runtime.GOOS {
				// The hook is for host platform
			} else {
				// Skip a hook which is not native
				outputLogger.Println("  - Non native build hook '%s': Skipping.", hookIdentifier)
				return nil
			}
		}
	}

	outputLogger.Print("  - Executing %s build hook '%s': ", hookName, hookIdentifier)
	args := strings.Split(buildHook, " ")
	for i, arg := range args {
		newArg := argReplacements[arg]
		if newArg == "" {
			continue
		}
		args[i] = newArg
	}

	if options.Verbosity == VERBOSE {
		outputLogger.Println("%s", strings.Join(args, " "))
	}

	stdout, stderr, err := shell.RunCommand(options.BinDirectory, args[0], args[1:]...)
	if options.Verbosity == VERBOSE {
		println(stdout)
	}
	if err != nil {
		return fmt.Errorf("%s - %s", err.Error(), stderr)
	}
	outputLogger.Println("Done.")

	return nil
}
