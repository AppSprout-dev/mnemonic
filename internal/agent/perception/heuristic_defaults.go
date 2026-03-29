package perception

// Default filter lists compiled into the binary. Users can extend these
// via the Extra* config fields in HeuristicsConfig without replacing them.

var defaultIgnoredPatterns = []string{
	".git/", "node_modules/", "__pycache__/", ".DS_Store", "~", ".swp", ".tmp", ".xbel",
	"venv/", ".venv/", "site-packages/", ".tox/", ".mypy_cache/", ".ruff_cache/", ".pytest_cache/",
	".egg-info/", ".eggs/",
}

var defaultLockfileNames = []string{
	"go.sum", "package-lock.json", "yarn.lock", "Cargo.lock",
	"poetry.lock", "pnpm-lock.yaml", "Gemfile.lock", "composer.lock",
	".release-please-manifest.json", "CHANGELOG.md",
}

var defaultAppInternalDirs = []string{
	"/google-chrome/", "/chromium/", "/BraveSoftware/",
	"/LM Studio/", "/lm-studio/",
	"/Trash/", "/.local/share/Trash/",
	"/leveldb/", "/IndexedDB/", "/Local Storage/", "/Session Storage/",
	"/Cache/", "/GPUCache/", "/ShaderCache/", "/Code Cache/",
	"/dconf/", "/gconf/",
	"/pulse/", "/pipewire/", "/wireplumber/",
	"/gvfs-metadata/", "/tracker3/",
	"session_migration-",
	"/.copilot/", "/.github-copilot/",
	"/snap/", "/.snap/",
	"/.config/gtk-", "/.config/dbus-",
	"/.mnemonic/", "/.claude/",
}

var defaultSensitiveNames = []string{
	".env", "id_rsa", "id_ed25519", "id_ecdsa", ".pem", ".key",
	"credentials", "secret", ".keychain", ".keystore", ".netrc", ".htpasswd",
}

var defaultSourceExtensions = []string{
	".go", ".py", ".js", ".ts", ".java", ".rs", ".cpp", ".c", ".h",
}

var defaultTrivialCommands = []string{
	"cd", "ls", "pwd", "clear", "exit", "history", "which", "whoami", "echo",
}

var defaultHighSignalCommands = []string{
	"git", "make", "go", "npm", "docker", "kubectl", "ssh", "curl", "python", "node",
}

var defaultCodeIndicators = []string{
	"{", "}", "function", "def", "class", "import", "package",
}

var defaultHighSignalKeywords = []string{
	"error", "bug", "fix", "todo", "hack", "important", "decision", "deadline", "meeting",
}

var defaultMediumSignalKeywords = []string{
	"config", "deploy", "release", "review", "merge", "refactor", "test", "fail",
}

var defaultLowSignalKeywords = []string{
	"update", "change", "add", "remove", "create", "install",
}

// mergeToSet builds a bool map from defaults and extras.
func mergeToSet(defaults, extras []string) map[string]bool {
	m := make(map[string]bool, len(defaults)+len(extras))
	for _, s := range defaults {
		m[s] = true
	}
	for _, s := range extras {
		m[s] = true
	}
	return m
}
