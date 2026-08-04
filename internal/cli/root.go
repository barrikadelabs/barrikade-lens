package cli

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	lensconfig "github.com/barrikadelabs/barrikade-lens/internal/config"
	"github.com/barrikadelabs/barrikade-lens/internal/detector"
	"github.com/barrikadelabs/barrikade-lens/internal/exporter"
	"github.com/barrikadelabs/barrikade-lens/internal/hubclient"
	"github.com/barrikadelabs/barrikade-lens/internal/managed"
	"github.com/barrikadelabs/barrikade-lens/internal/probe"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/endpoint"
	repositoryscanner "github.com/barrikadelabs/barrikade-lens/internal/scanner/repository"
	servicecontrol "github.com/barrikadelabs/barrikade-lens/internal/service"
	"github.com/barrikadelabs/barrikade-lens/internal/tui"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/spf13/cobra"
)

var Version = "2.0.0-dev"

type Dependencies struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

func Execute() int {
	return ExecuteWith(Dependencies{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}, os.Args[1:])
}

func ExecuteWith(dependencies Dependencies, args []string) int {
	root := newRoot(dependencies)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		var exitError exitError
		if errors.As(err, &exitError) {
			if exitError.message != "" {
				fmt.Fprintln(dependencies.Err, exitError.message)
			}
			return exitError.code
		}
		fmt.Fprintln(dependencies.Err, "Error:", err)
		return 1
	}
	return 0
}

func newRoot(dependencies Dependencies) *cobra.Command {
	var organizationID, packPath string
	command := &cobra.Command{
		Use: "barrikade-lens", Short: "Discover autonomous agents and their dependencies",
		Long:    "Barrikade Lens builds a local-first, evidence-backed inventory of agents, runtimes, MCP servers, skills, models, APIs, repositories, and deployments.",
		Version: Version, SilenceUsage: true, SilenceErrors: true,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			pack, err := loadPack(packPath)
			if err != nil {
				return err
			}
			snapshot, err := endpoint.Scan(command.Context(), endpoint.Options{OrganizationID: organizationID, Pack: pack})
			if err != nil {
				return err
			}
			if repositorySnapshot, repoErr := repositoryscanner.Scan(command.Context(), repositoryscanner.Options{OrganizationID: organizationID, Root: ".", Pack: pack}); repoErr == nil {
				if err := discovery.MergeSnapshots(&snapshot, repositorySnapshot); err != nil {
					return err
				}
			} else {
				snapshot.Coverage.Partial = true
				snapshot.Errors = append(snapshot.Errors, discovery.ScanError{DetectorID: "repository", Code: "project_scan_failed", Message: "The current project could not be fully inspected"})
			}
			if err := snapshot.Validate(); err != nil {
				return err
			}
			if isTerminal(dependencies.In) && isTerminal(dependencies.Out) {
				if err := tui.Run(snapshot, dependencies.In, dependencies.Out); err != nil {
					return err
				}
			} else if err := exporter.Write(dependencies.Out, snapshot, exporter.FormatJSON); err != nil {
				return err
			}
			if snapshot.Coverage.Partial {
				return exitError{code: 2}
			}
			return nil
		},
	}
	command.SetIn(dependencies.In)
	command.SetOut(dependencies.Out)
	command.SetErr(dependencies.Err)
	command.PersistentFlags().StringVar(&organizationID, "organization", "local", "organization identity used to salt stable IDs")
	command.PersistentFlags().StringVar(&packPath, "detector-pack", "", "load a declarative detector pack from a local YAML file")
	command.AddCommand(newScanCommand(dependencies, &organizationID, &packPath), newEnrollCommand(dependencies), newServiceCommand(dependencies), newDoctorCommand(dependencies, &packPath))
	return command
}

func newScanCommand(dependencies Dependencies, organizationID, packPath *string) *cobra.Command {
	var scope, format, root, output string
	var probeURLs, allowedProbeHosts []string
	command := &cobra.Command{
		Use: "scan", Short: "Run an endpoint or repository discovery scan", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			if !exporter.ValidFormat(format) {
				return fmt.Errorf("--format must be human, json, ndjson, or cyclonedx")
			}
			pack, err := loadPack(*packPath)
			if err != nil {
				return err
			}
			var snapshot discovery.Snapshot
			switch strings.ToLower(scope) {
			case "endpoint":
				snapshot, err = endpoint.Scan(command.Context(), endpoint.Options{OrganizationID: *organizationID, Pack: pack})
			case "repo", "repository":
				snapshot, err = repositoryscanner.Scan(command.Context(), repositoryscanner.Options{OrganizationID: *organizationID, Root: root, Pack: pack})
			default:
				return fmt.Errorf("--scope must be endpoint or repo")
			}
			if err != nil {
				return err
			}
			for _, target := range probeURLs {
				result, probeErr := probe.Handshake(command.Context(), target, probe.Config{AllowedHosts: allowedProbeHosts})
				if probeErr != nil {
					snapshot.Coverage.Partial = true
					snapshot.Errors = append(snapshot.Errors, discovery.ScanError{DetectorID: "active.metadata", Code: "probe_failed", Message: probeErr.Error()})
					continue
				}
				probe.Apply(&snapshot, result)
			}
			if err := snapshot.Validate(); err != nil {
				return err
			}
			writer := dependencies.Out
			var file *os.File
			if output != "" {
				file, err = os.OpenFile(output, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
				if err != nil {
					return err
				}
				defer file.Close()
				writer = file
			}
			if err := exporter.Write(writer, snapshot, exporter.Format(format)); err != nil {
				return err
			}
			if snapshot.Coverage.Partial {
				return exitError{code: 2}
			}
			return nil
		},
	}
	command.Flags().StringVar(&scope, "scope", "endpoint", "discovery scope: endpoint or repo")
	command.Flags().StringVar(&format, "format", "human", "output format: human, json, ndjson, or cyclonedx")
	command.Flags().StringVar(&root, "path", ".", "repository root for --scope repo")
	command.Flags().StringVarP(&output, "output", "o", "", "write the export to a private local file")
	command.Flags().StringSliceVar(&probeURLs, "probe-url", nil, "opt in to a metadata-only handshake against an already-running HTTP endpoint")
	command.Flags().StringSliceVar(&allowedProbeHosts, "allow-probe-host", nil, "explicit host allowlist for active metadata handshakes")
	return command
}

func newEnrollCommand(dependencies Dependencies) *cobra.Command {
	var hubURL, configPath string
	command := &cobra.Command{
		Use: "enroll [code]", Short: "Enroll this endpoint with a Lens Hub", Args: cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			code := ""
			if len(args) == 1 {
				code = args[0]
			}
			if code == "" {
				code = os.Getenv("BARRIKADE_LENS_ENROLLMENT_CODE")
			}
			if code == "" {
				return fmt.Errorf("an enrollment code is required")
			}
			if hubURL == "" {
				hubURL = os.Getenv("BARRIKADE_LENS_HUB")
			}
			if hubURL == "" {
				return fmt.Errorf("--hub is required")
			}
			cfg, err := hubclient.New(Version).Enroll(command.Context(), hubURL, code, configPath)
			if err != nil {
				return err
			}
			if err := lensconfig.Save(configPath, cfg); err != nil {
				return err
			}
			path := configPath
			if path == "" {
				path, _ = lensconfig.Path()
			}
			fmt.Fprintf(dependencies.Out, "Enrolled %s with Lens Hub.\nConfiguration saved privately at %s.\nRun `barrikade-lens service install` to enable managed discovery.\n", cfg.SourceID, path)
			return nil
		},
	}
	command.Flags().StringVar(&hubURL, "hub", "", "Lens Hub base URL")
	command.Flags().StringVar(&configPath, "config", "", "managed collector configuration path")
	return command
}

func newServiceCommand(dependencies Dependencies) *cobra.Command {
	var configPath string
	command := &cobra.Command{Use: "service", Short: "Manage the background endpoint collector"}
	command.PersistentFlags().StringVar(&configPath, "config", "", "managed collector configuration path")
	command.AddCommand(&cobra.Command{Use: "install", Short: "Install and start the managed collector", Args: cobra.NoArgs, RunE: func(command *cobra.Command, args []string) error {
		if configPath == "" {
			configPath, _ = lensconfig.Path()
		}
		if _, err := lensconfig.Load(configPath); err != nil {
			return fmt.Errorf("enroll this endpoint before installing the service: %w", err)
		}
		status, err := servicecontrol.Install(command.Context(), "", configPath)
		if err != nil {
			return err
		}
		fmt.Fprintf(dependencies.Out, "Collector service: %s\nDefinition: %s\n", status.State, status.DefinitionPath)
		return nil
	}})
	command.AddCommand(&cobra.Command{Use: "status", Short: "Show managed collector status", Args: cobra.NoArgs, RunE: func(command *cobra.Command, args []string) error {
		status := servicecontrol.GetStatus(command.Context())
		fmt.Fprintf(dependencies.Out, "Collector service: %s\n", status.State)
		if status.DefinitionPath != "" {
			fmt.Fprintf(dependencies.Out, "Definition: %s\n", status.DefinitionPath)
		}
		return nil
	}})
	command.AddCommand(&cobra.Command{Use: "uninstall", Short: "Stop and uninstall the managed collector", Args: cobra.NoArgs, RunE: func(command *cobra.Command, args []string) error {
		if err := servicecontrol.Uninstall(command.Context()); err != nil {
			return err
		}
		fmt.Fprintln(dependencies.Out, "Collector service uninstalled. Local configuration was preserved.")
		return nil
	}})
	run := &cobra.Command{Use: "run", Hidden: true, Args: cobra.NoArgs, RunE: func(command *cobra.Command, args []string) error {
		if configPath == "" {
			configPath, _ = lensconfig.Path()
		}
		return managed.Runner{ConfigPath: configPath, Version: Version}.Run(command.Context())
	}}
	command.AddCommand(run)
	return command
}

func newDoctorCommand(dependencies Dependencies, packPath *string) *cobra.Command {
	var hubURL string
	command := &cobra.Command{
		Use: "doctor", Short: "Check permissions, detector pack, managed configuration, and Hub connectivity", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			failed := false
			pack, err := loadPack(*packPath)
			if err != nil {
				fmt.Fprintf(dependencies.Out, "FAIL  detector pack: %v\n", err)
				failed = true
			} else {
				fmt.Fprintf(dependencies.Out, "OK    detector pack %s %s (%d runtimes, %d frameworks)\n", pack.ID, pack.Version, len(pack.Runtimes), len(pack.Frameworks))
			}
			home, err := os.UserHomeDir()
			if err != nil {
				fmt.Fprintf(dependencies.Out, "FAIL  home directory: %v\n", err)
				failed = true
			} else if info, statErr := os.Stat(home); statErr != nil || !info.IsDir() {
				fmt.Fprintln(dependencies.Out, "FAIL  home directory is unavailable")
				failed = true
			} else {
				fmt.Fprintln(dependencies.Out, "OK    endpoint filesystem access")
			}
			path, _ := lensconfig.Path()
			cfg, cfgErr := lensconfig.Load(path)
			if cfgErr == nil {
				fmt.Fprintf(dependencies.Out, "OK    managed configuration %s\n", path)
				if hubURL == "" {
					hubURL = cfg.HubURL
				}
			} else {
				fmt.Fprintln(dependencies.Out, "INFO  endpoint is not enrolled; standalone scans remain available")
			}
			if hubURL != "" {
				request, reqErr := http.NewRequestWithContext(command.Context(), http.MethodGet, strings.TrimSuffix(hubURL, "/")+"/health", nil)
				if reqErr != nil {
					return reqErr
				}
				client := http.Client{Timeout: 5 * time.Second}
				response, requestErr := client.Do(request)
				if requestErr != nil {
					fmt.Fprintln(dependencies.Out, "FAIL  Lens Hub connectivity")
					failed = true
				} else {
					response.Body.Close()
					if response.StatusCode/100 != 2 {
						fmt.Fprintln(dependencies.Out, "FAIL  Lens Hub connectivity")
						failed = true
					} else {
						fmt.Fprintln(dependencies.Out, "OK    Lens Hub connectivity")
					}
				}
			}
			if failed {
				return exitError{code: 1, message: "One or more diagnostics failed."}
			}
			return nil
		},
	}
	command.Flags().StringVar(&hubURL, "hub", "", "Lens Hub URL to test")
	return command
}

func loadPack(path string) (detector.Pack, error) {
	if path != "" {
		return detector.Load(path)
	}
	return detector.Builtin()
}
func isTerminal(value any) bool {
	file, ok := value.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

type exitError struct {
	code    int
	message string
}

func (e exitError) Error() string { return e.message }
