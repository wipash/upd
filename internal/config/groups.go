package config

// Group is a heading and the built-in jobs listed under it in the starter
// config.
type Group struct {
	Title string
	Names []string
}

// BuiltinGroups lists every built-in job once, in the order of builtin.toml.
var BuiltinGroups = []Group{
	{"System package managers (only with -a)", []string{
		"apt", "dnf", "apk", "pacman", "zypper-dup", "zypper-update", "snap",
		"rpm-ostree", "rpm-ostree-ssh", "aws-system",
	}},
	{"Package managers", []string{
		"flatpak", "brew", "uv-self", "uv", "pipx", "rustup", "mise", "mise-self",
		"gh-extensions", "volta", "upd",
	}},
	{"Tools that update themselves (skipped when a package manager installed them)", []string{
		"claude", "codex", "copilot", "omp", "pi", "herdr", "herdr-core", "agy",
		"opencode", "cursor-agent", "amp", "goose", "deno", "bun", "pnpm", "chezmoi",
		"rclone", "yt-dlp", "gcloud", "az", "flyctl", "ghcup", "juliaup", "sdkman",
		"aws",
	}},
}
