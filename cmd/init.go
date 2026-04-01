package cmd

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/term"
	"github.com/frostyard/clix"
	"github.com/frostyard/intuneme/internal/config"
	"github.com/frostyard/intuneme/internal/prereq"
	"github.com/frostyard/intuneme/internal/provision"
	"github.com/frostyard/intuneme/internal/puller"
	"github.com/frostyard/intuneme/internal/runner"
	"github.com/frostyard/intuneme/internal/sudoers"
	pkgversion "github.com/frostyard/intuneme/internal/version"
	"github.com/spf13/cobra"
)

var forceInit bool
var passwordFile string
var insidersInit bool
var tmpDirInit string
var authStackInit string
var himmelblauDomain string
var himmelblauEmail string
var containerToolInit string
var localImageInit bool
var machineNameInit string

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Provision the Intune nspawn container",
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

		// Validate auth stack flag.
		stack := config.AuthStack(authStackInit)
		if !stack.IsValid() {
			return fmt.Errorf("invalid --auth-stack %q; must be one of: intune, himmelblau", authStackInit)
		}

		// Himmelblau requires --himmelblau-domain and --himmelblau-email.
		if stack == config.AuthStackHimmelblau {
			if himmelblauDomain == "" {
				return fmt.Errorf("--himmelblau-domain is required when using --auth-stack himmelblau")
			}
			if himmelblauEmail == "" {
				return fmt.Errorf("--himmelblau-email is required when using --auth-stack himmelblau")
			}
			if insidersInit {
				return fmt.Errorf("--insiders is not available for the himmelblau auth stack")
			}
		}

		// Validate machine name early (before password prompt).
		if machineNameInit != "" && !isValidMachineName(machineNameInit) {
			return fmt.Errorf("invalid machine name %q: use only letters, digits, and hyphens", machineNameInit)
		}

		// Check prerequisites
		if errs := prereq.Check(r); len(errs) > 0 {
			for _, e := range errs {
				rep.Warning("  - %s", e)
			}
			return fmt.Errorf("missing prerequisites")
		}

		// Resolve host user early — needed for password validation.
		u, err := user.Current()
		if err != nil {
			return fmt.Errorf("get current user: %w", err)
		}

		// Acquire and validate password before doing any container work.
		password, err := readPassword(u.Username, passwordFile)
		if err != nil {
			return err
		}

		// Load config for dry-run reporting.
		cfg, err := config.Load(root)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		if clix.DryRun {
			rep.Message("[dry-run] Would pull OCI image (%s stack) and create container at %s", stack, cfg.RootfsPath)
			rep.Message("[dry-run] Would create container user %s", u.Username)
			return nil
		}

		// Create ~/Intune directory
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot determine home directory: %w", err)
		}
		intuneHome := filepath.Join(home, "Intune")
		if err := os.MkdirAll(intuneHome, 0755); err != nil {
			return fmt.Errorf("create ~/Intune: %w", err)
		}

		// Check if already initialized
		if _, err := os.Stat(cfg.RootfsPath); err == nil && !forceInit {
			return fmt.Errorf("already initialized at %s — use --force to reinitialize", root)
		}

		cfg.Insiders = insidersInit
		cfg.AuthStack = stack
		if machineNameInit != "" {
			cfg.MachineName = machineNameInit
		}
		if stack == config.AuthStackHimmelblau {
			cfg.HimmelblauDomain = himmelblauDomain
			cfg.HimmelblauEmail = himmelblauEmail
		}
		image := pkgversion.ImageRefForStack(string(stack), cfg.Insiders)

		p, err := puller.DetectByName(r, containerToolInit)
		if err != nil {
			return err
		}

		if err := os.MkdirAll(cfg.RootfsPath, 0755); err != nil {
			return fmt.Errorf("create rootfs dir: %w", err)
		}
		if localImageInit {
			rep.Message("Extracting local image %s (via %s)...", image, p.Name())
			if err := p.ExtractLocal(r, image, cfg.RootfsPath, tmpDirInit); err != nil {
				return err
			}
		} else {
			rep.Message("Pulling and extracting OCI image %s (via %s)...", image, p.Name())
			if err := p.PullAndExtract(r, image, cfg.RootfsPath, tmpDirInit); err != nil {
				return err
			}
		}

		hostname, _ := os.Hostname()

		if err := provision.ProvisionContainer(r, rep, cfg.RootfsPath, u.Username, os.Getuid(), os.Getgid(), hostname, stack); err != nil {
			return err
		}

		// Write Himmelblau configuration files into rootfs.
		if stack == config.AuthStackHimmelblau {
			rep.Message("Writing Himmelblau configuration (domain: %s)...", cfg.HimmelblauDomain)
			if err := provision.WriteHimmelblauConfig(r, cfg.RootfsPath, cfg.HimmelblauDomain, cfg.HimmelblauEmail, u.Username); err != nil {
				return fmt.Errorf("write himmelblau config: %w", err)
			}
		}

		rep.Message("Setting container user password...")
		bypassPAM := stack == config.AuthStackHimmelblau
		if err := provision.SetContainerPassword(r, cfg.RootfsPath, u.Username, password, bypassPAM); err != nil {
			return fmt.Errorf("set password failed: %w", err)
		}

		if clix.Verbose {
			rep.Message("Installing sudoers rule for passwordless app launch...")
		}
		if err := sudoers.Install(r, u.Username); err != nil {
			rep.Warning("sudoers install failed: %v", err)
		}

		if provision.SELinuxEnabled() {
			if clix.Verbose {
				rep.Message("Applying SELinux policy (required for machinectl shell on SELinux systems)...")
			}
			if err := provision.InstallSELinuxPolicy(r, cfg.RootfsPath); err != nil {
				rep.Warning("SELinux policy setup failed: %v", err)
			}
		}

		if clix.Verbose {
			rep.Message("Saving config...")
		}
		cfg.HostUID = os.Getuid()
		cfg.HostUser = u.Username
		if err := cfg.Save(root); err != nil {
			return err
		}

		rep.Message("Initialized intuneme at %s (auth stack: %s)", root, cfg.AuthStack)
		if cfg.AuthStack == config.AuthStackHimmelblau {
			rep.Message("")
			rep.Message("Next steps for Himmelblau enrollment:")
			rep.Message("  1. Start the container: intuneme start")
			rep.Message("  2. Open a shell: intuneme shell")
			rep.Message("  3. Enroll with: aad-tool auth-test --name %s", u.Username)
			rep.Message("  4. Follow the FIDO2/MFA prompts to complete enrollment")
			rep.Message("")
			rep.Message("Tip: use the same password as your container user for the Himmelblau PIN")
			rep.Message("     to enable auto-unlock on login.")
		}
		return nil
	},
}

// validatePassword checks the password against the same rules enforced by the
// container's pam_pwquality.so configuration (minlen=12, dcredit/ucredit/lcredit/ocredit=-1,
// usercheck=1). All failures are collected and returned together.
func validatePassword(username, password string) error {
	var errs []string
	if len([]rune(password)) < 12 {
		errs = append(errs, "must be at least 12 characters")
	}
	var hasDigit, hasUpper, hasLower, hasSpecial bool
	for _, r := range password {
		switch {
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			hasSpecial = true
		}
	}
	if !hasDigit {
		errs = append(errs, "must contain at least one digit")
	}
	if !hasUpper {
		errs = append(errs, "must contain at least one uppercase letter")
	}
	if !hasLower {
		errs = append(errs, "must contain at least one lowercase letter")
	}
	if !hasSpecial {
		errs = append(errs, "must contain at least one special character")
	}
	if username != "" && strings.Contains(strings.ToLower(password), strings.ToLower(username)) {
		errs = append(errs, "must not contain your username")
	}
	if len(errs) > 0 {
		return fmt.Errorf("password requirements not met:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// readPassword acquires and validates the container user password.
// If passwordFile is non-empty, it reads the first line of that file.
// Otherwise it prompts the user interactively (without echo), asking twice
// for confirmation. Up to 3 mismatch attempts are allowed.
func readPassword(username, passwordFile string) (string, error) {
	if passwordFile != "" {
		data, err := os.ReadFile(passwordFile)
		if err != nil {
			return "", fmt.Errorf("read password file: %w", err)
		}
		// Use only the first line; trim surrounding whitespace.
		first, _, _ := strings.Cut(strings.TrimRight(string(data), "\r\n"), "\n")
		password := strings.TrimSpace(first)
		if err := validatePassword(username, password); err != nil {
			return "", err
		}
		return password, nil
	}

	for range 3 {
		fmt.Print("Enter container user password: ")
		p1, err := term.ReadPassword(os.Stdin.Fd())
		fmt.Println()
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}

		fmt.Print("Confirm password: ")
		p2, err := term.ReadPassword(os.Stdin.Fd())
		fmt.Println()
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}

		if string(p1) != string(p2) {
			rep.Warning("Passwords do not match, please try again.")
			continue
		}

		if err := validatePassword(username, string(p1)); err != nil {
			return "", err
		}
		return string(p1), nil
	}
	return "", fmt.Errorf("passwords did not match after 3 attempts")
}

// isValidMachineName checks that a machine name is a valid hostname label:
// letters, digits, and hyphens, not starting or ending with a hyphen.
func isValidMachineName(name string) bool {
	if name == "" {
		return false
	}
	if name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' {
			return false
		}
	}
	return true
}

func init() {
	initCmd.Flags().BoolVar(&forceInit, "force", false, "reinitialize even if already set up")
	initCmd.Flags().StringVar(&passwordFile, "password-file", "", "path to file containing the container user password (first line used)")
	initCmd.Flags().BoolVar(&insidersInit, "insiders", false, "use the insiders channel container image")
	initCmd.Flags().StringVar(&tmpDirInit, "tmp-dir", "", "directory for temporary files during image extraction (default: system temp dir)")
	initCmd.Flags().StringVar(&authStackInit, "auth-stack", "intune", "authentication stack: intune or himmelblau")
	initCmd.Flags().StringVar(&himmelblauDomain, "himmelblau-domain", "", "Entra domain for Himmelblau enrollment (required with --auth-stack himmelblau)")
	initCmd.Flags().StringVar(&himmelblauEmail, "himmelblau-email", "", "Entra email address for Himmelblau user mapping (required with --auth-stack himmelblau)")
	initCmd.Flags().StringVar(&containerToolInit, "container-tool", "", "container tool to use for image operations: podman, docker, or skopeo (default: auto-detect)")
	initCmd.Flags().BoolVar(&localImageInit, "local-image", false, "use a locally available image instead of pulling from the registry")
	initCmd.Flags().StringVar(&machineNameInit, "machine-name", "", "container machine name (default: intuneme)")
	rootCmd.AddCommand(initCmd)
}
