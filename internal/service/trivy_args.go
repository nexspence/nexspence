package service

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
