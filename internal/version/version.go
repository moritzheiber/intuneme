package version

import "regexp"

// Version is set from main.go at startup via ldflags.
var Version = "dev"

const (
	intuneImageBase     = "ghcr.io/frostyard/ubuntu-intune"
	himmelblauImageBase = "ghcr.io/frostyard/ubuntu-himmelblau"
)

var semverRe = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)$`)

// ImageRef returns the full OCI image reference for the container.
// Release versions (clean semver) get a pinned tag; everything else gets latest.
// When insiders is true, the tag is always "insiders".
func ImageRef(insiders bool) string {
	return imageRef(intuneImageBase, insiders)
}

// HimmelblauImageRef returns the OCI image reference for the Himmelblau container.
func HimmelblauImageRef(insiders bool) string {
	return imageRef(himmelblauImageBase, insiders)
}

// ImageRefForStack returns the OCI image reference for the given auth stack.
func ImageRefForStack(authStack string, insiders bool) string {
	if authStack == "himmelblau" {
		return HimmelblauImageRef(insiders)
	}
	return ImageRef(insiders)
}

func imageRef(base string, insiders bool) string {
	if insiders {
		return base + ":insiders"
	}
	m := semverRe.FindStringSubmatch(Version)
	if m == nil {
		return base + ":latest"
	}
	return base + ":v" + m[1]
}
