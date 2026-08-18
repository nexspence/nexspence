package service

import "strings"

// TrivyOptions is everything the scanner needs from configuration: where the
// operator's binary is, and where that binary should read its vulnerability
// database from.
//
// It mirrors config.TrivyConfig without importing it — the service layer is
// reachable from tests and from the CLI, neither of which should have to build
// a whole configuration to run a scan.
//
// An empty DB/cache value means "do not pass the corresponding flag", which
// leaves Trivy's own default in force. Bin is the exception: it defaults to
// "trivy" resolved through PATH.
type TrivyOptions struct {
	Enabled          bool
	Bin              string
	DBRepository     []string
	JavaDBRepository []string
	SkipDBUpdate     bool
	CacheDir         string
}

// BinOrDefault is the executable to run: the configured path, or "trivy"
// resolved through PATH.
func (o TrivyOptions) BinOrDefault() string {
	if o.Bin == "" {
		return "trivy"
	}
	return o.Bin
}

// TrivyScanArgs builds the argv for one image scan.
//
// Credentials are deliberately absent: they travel in the environment (see
// TrivyEnv), because anything in argv is readable through the process table,
// and Trivy's own help says as much about --password.
func TrivyScanArgs(o TrivyOptions, imageRef string, insecureRegistry bool) []string {
	args := []string{
		"image",
		"--format", "json",
		"--exit-code", "0", // do not use non-zero exit when CVEs exist; we rely on JSON
		"--quiet",
		"--no-progress",
		"--image-src", "remote", // no Docker/containerd socket inside the container
	}
	if insecureRegistry {
		args = append(args, "--insecure")
	}
	if len(o.DBRepository) > 0 {
		args = append(args, "--db-repository", strings.Join(o.DBRepository, ","))
	}
	if len(o.JavaDBRepository) > 0 {
		args = append(args, "--java-db-repository", strings.Join(o.JavaDBRepository, ","))
	}
	if o.SkipDBUpdate {
		// Both databases: an air-gapped deployment that pre-seeds one and lets the
		// other reach out would fail on the Java scan alone, which reads as a
		// broken scanner rather than as a deliberate setting.
		args = append(args, "--skip-db-update", "--skip-java-db-update")
	}
	if o.CacheDir != "" {
		args = append(args, "--cache-dir", o.CacheDir)
	}
	return append(args, imageRef)
}

// TrivyEnv returns the child environment for a Trivy run: the parent
// environment plus registry credentials, which must not be passed as flags.
func TrivyEnv(parent []string, username, password string) []string {
	env := append([]string(nil), parent...)
	if username != "" {
		env = append(env, "TRIVY_USERNAME="+username)
	}
	if password != "" {
		env = append(env, "TRIVY_PASSWORD="+password)
	}
	return env
}
