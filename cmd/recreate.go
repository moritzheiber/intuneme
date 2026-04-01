package cmd

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"github.com/frostyard/clix"
	"github.com/frostyard/intuneme/internal/broker"
	"github.com/frostyard/intuneme/internal/config"
	"github.com/frostyard/intuneme/internal/nspawn"
	"github.com/frostyard/intuneme/internal/provision"
	"github.com/frostyard/intuneme/internal/puller"
	"github.com/frostyard/intuneme/internal/runner"
	pkgversion "github.com/frostyard/intuneme/internal/version"
	"github.com/spf13/cobra"
)

var insidersRecreate bool
var tmpDirRecreate string
var containerToolRecreate string
var localImageRecreate bool

var recreateCmd = &cobra.Command{
	Use:   "recreate",
	Short: "Recreate the container with a fresh image, preserving enrollment state",
	RunE: func(cmd *cobra.Command, args []string) error {
		r := &runner.SystemRunner{}
		root := rootDir
		if root == "" {
			var err error
			root, err = config.DefaultRoot()
			if err != nil {
				return err
			}
		}

		cfg, err := config.Load(root)
		if err != nil {
			return err
		}

		// Verify initialized
		if _, err := os.Stat(cfg.RootfsPath); err != nil {
			return fmt.Errorf("not initialized — run 'intuneme init' first")
		}

		u, err := user.Current()
		if err != nil {
			return fmt.Errorf("get current user: %w", err)
		}

		if clix.DryRun {
			rep.Message("[dry-run] Would stop container (if running)")
			rep.Message("[dry-run] Would backup shadow entry and broker state")
			rep.Message("[dry-run] Would remove old rootfs at %s", cfg.RootfsPath)
			rep.Message("[dry-run] Would pull new image and re-provision")
			return nil
		}

		// Validate sudo early
		rep.Message("Checking sudo credentials...")
		if err := nspawn.ValidateSudo(r); err != nil {
			return fmt.Errorf("sudo authentication failed: %w", err)
		}

		// Stop container if running
		if nspawn.IsRunning(r, cfg.MachineName) {
			if cfg.BrokerProxy {
				pidPath := filepath.Join(root, "broker-proxy.pid")
				broker.StopByPIDFile(pidPath)
				rep.Message("Broker proxy stopped.")
			}
			rep.Message("Stopping container...")
			if err := nspawn.Stop(r, cfg.MachineName); err != nil {
				return fmt.Errorf("failed to stop container: %w", err)
			}
			rep.Message("Container stopped.")
		}

		// Backup state
		if clix.Verbose {
			rep.Message("Backing up shadow entry...")
		}
		shadowLine, err := provision.BackupShadowEntry(r, cfg.RootfsPath, u.Username)
		if err != nil {
			return fmt.Errorf("backup shadow entry: %w", err)
		}

		// Backup enrollment state (auth-stack-aware).
		var brokerBackupDir string
		if cfg.AuthStack == config.AuthStackHimmelblau {
			if clix.Verbose {
				rep.Message("Backing up Himmelblau state...")
			}
			brokerBackupDir, err = provision.BackupHimmelblauState(r, cfg.RootfsPath)
		} else {
			if clix.Verbose {
				rep.Message("Backing up device broker state...")
			}
			brokerBackupDir, err = provision.BackupDeviceBrokerState(r, cfg.RootfsPath)
		}
		if err != nil {
			return fmt.Errorf("backup enrollment state: %w", err)
		}
		if brokerBackupDir != "" {
			defer func() { _ = os.RemoveAll(brokerBackupDir) }()
			if clix.Verbose {
				rep.Message("Enrollment state backed up.")
			}
		} else {
			if clix.Verbose {
				rep.Message("No enrollment state found (skipping).")
			}
		}

		// Remove old rootfs
		rep.Message("Removing old rootfs at %s...", cfg.RootfsPath)
		out, err := r.Run("sudo", "rm", "-rf", cfg.RootfsPath)
		if err != nil {
			return fmt.Errorf("rm rootfs failed: %w\n%s", err, out)
		}

		// Pull new image
		if cmd.Flags().Changed("insiders") {
			if cfg.AuthStack == config.AuthStackHimmelblau && insidersRecreate {
				return fmt.Errorf("--insiders is not available for the himmelblau auth stack")
			}
			cfg.Insiders = insidersRecreate
			if err := cfg.Save(root); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
		}
		image := pkgversion.ImageRefForStack(string(cfg.AuthStack), cfg.Insiders)
		p, err := puller.DetectByName(r, containerToolRecreate)
		if err != nil {
			return err
		}

		if err := os.MkdirAll(cfg.RootfsPath, 0755); err != nil {
			return fmt.Errorf("create rootfs dir: %w", err)
		}
		if localImageRecreate {
			rep.Message("Extracting local image %s (via %s)...", image, p.Name())
			if err := p.ExtractLocal(r, image, cfg.RootfsPath, tmpDirRecreate); err != nil {
				return err
			}
		} else {
			rep.Message("Pulling and extracting OCI image %s (via %s)...", image, p.Name())
			if err := p.PullAndExtract(r, image, cfg.RootfsPath, tmpDirRecreate); err != nil {
				return err
			}
		}

		// Re-provision
		hostname, _ := os.Hostname()

		if err := provision.ProvisionContainer(r, rep, cfg.RootfsPath, u.Username, os.Getuid(), os.Getgid(), hostname, cfg.AuthStack); err != nil {
			return err
		}

		// Re-write Himmelblau configuration files after re-provision.
		if cfg.AuthStack == config.AuthStackHimmelblau {
			if clix.Verbose {
				rep.Message("Writing Himmelblau configuration (domain: %s)...", cfg.HimmelblauDomain)
			}
			if err := provision.WriteHimmelblauConfig(r, cfg.RootfsPath, cfg.HimmelblauDomain, cfg.HimmelblauEmail, u.Username); err != nil {
				return fmt.Errorf("write himmelblau config: %w", err)
			}
		}

		// Restore state
		if clix.Verbose {
			rep.Message("Restoring shadow entry...")
		}
		if err := provision.RestoreShadowEntry(r, cfg.RootfsPath, shadowLine); err != nil {
			return fmt.Errorf("restore shadow entry: %w", err)
		}

		if brokerBackupDir != "" {
			if clix.Verbose {
				rep.Message("Restoring enrollment state...")
			}
			var restoreErr error
			if cfg.AuthStack == config.AuthStackHimmelblau {
				restoreErr = provision.RestoreHimmelblauState(r, cfg.RootfsPath, brokerBackupDir)
			} else {
				restoreErr = provision.RestoreDeviceBrokerState(r, cfg.RootfsPath, brokerBackupDir)
			}
			if restoreErr != nil {
				rep.Warning("restore enrollment state failed: %v", restoreErr)
			}
		}

		rep.Message("Container recreated. Run 'intuneme start' to boot.")
		return nil
	},
}

func init() {
	recreateCmd.Flags().BoolVar(&insidersRecreate, "insiders", false, "switch to the insiders channel container image")
	recreateCmd.Flags().StringVar(&tmpDirRecreate, "tmp-dir", "", "directory for temporary files during image extraction (default: system temp dir)")
	recreateCmd.Flags().StringVar(&containerToolRecreate, "container-tool", "", "container tool to use for image operations: podman, docker, or skopeo (default: auto-detect)")
	recreateCmd.Flags().BoolVar(&localImageRecreate, "local-image", false, "use a locally available image instead of pulling from the registry")
	rootCmd.AddCommand(recreateCmd)
}
