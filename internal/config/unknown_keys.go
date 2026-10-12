package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// envPrefix is the prefix viper derives every environment override from.
const envPrefix = "NEXSPENCE_"

// envReadElsewhere are NEXSPENCE_* variables that something other than viper
// reads: the container entrypoint turns NEXSPENCE_JWT_SECRET_FILE into
// NEXSPENCE_AUTH_JWT_SECRET before the server starts.
var envReadElsewhere = map[string]bool{"NEXSPENCE_JWT_SECRET_FILE": true}

// UnknownKeys lists settings the server was given but does not read: config
// file keys that match no field, and NEXSPENCE_* environment variables that
// match no key. viper ignores both without a word, so a camelCase key copied
// from the Helm chart's values.yaml (docker.subdomainConnector) or an env name
// spelled from it (NEXSPENCE_DOCKER_SUBDOMAINCONNECTOR_ENABLED) leaves the
// feature off while the operator's config plainly says otherwise (#660).
//
// unusedFileKeys are the leftovers of decoding the config file (mapstructure's
// Metadata.Unused); environ is os.Environ(). Each entry names the key and,
// when one differs only in underscores or case, the key that was meant.
func UnknownKeys(unusedFileKeys, environ []string) []string {
	known := knownKeys()

	var out []string
	for _, k := range unusedFileKeys {
		msg := fmt.Sprintf("config key %q is not a setting and is ignored", k)
		if s := known.suggest(k); s != "" {
			msg += fmt.Sprintf(" (did you mean %q?)", s)
		}
		out = append(out, msg)
	}
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, envPrefix) || envReadElsewhere[name] || known.coversEnv(name) {
			continue
		}
		msg := fmt.Sprintf("environment variable %s matches no setting and is ignored", name)
		if s := known.suggest(strings.TrimPrefix(name, envPrefix)); s != "" {
			msg += fmt.Sprintf(" (did you mean %s?)", envName(s))
		}
		out = append(out, msg)
	}
	sort.Strings(out)
	return out
}

// keySet is every config path the Config struct defines.
type keySet struct {
	leaves map[string]bool // settable keys, e.g. "docker.subdomain_connector.enabled"
	maps   map[string]bool // map-valued keys, which accept any key below them
	all    []string        // leaves, maps and the sections between them
}

func knownKeys() keySet {
	ks := keySet{leaves: map[string]bool{}, maps: map[string]bool{}}
	ks.walk(reflect.TypeOf(Config{}), "")
	return ks
}

func (ks *keySet) walk(t reflect.Type, prefix string) {
	for i := range t.NumField() {
		f := t.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("mapstructure"), ",")
		if tag == "-" || !f.IsExported() {
			continue
		}
		if tag == "" {
			tag = strings.ToLower(f.Name)
		}
		key := joinDotted(prefix, tag)
		ks.all = append(ks.all, key)
		switch ft := f.Type; {
		case ft.Kind() == reflect.Struct && ft.PkgPath() == t.PkgPath():
			ks.walk(ft, key)
		case ft.Kind() == reflect.Map:
			ks.maps[key] = true
		default:
			ks.leaves[key] = true
		}
	}
}

// coversEnv reports whether viper would map the variable onto a key.
func (ks keySet) coversEnv(name string) bool {
	for k := range ks.leaves {
		if envName(k) == name {
			return true
		}
	}
	for k := range ks.maps {
		if e := envName(k); name == e || strings.HasPrefix(name, e+"_") {
			return true
		}
	}
	return false
}

// suggest returns the known path that k names when underscores, dots and
// case are ignored, or "" when there is none.
func (ks keySet) suggest(k string) string {
	want := squash(k)
	for _, cand := range ks.all {
		if squash(cand) == want {
			return cand
		}
	}
	return ""
}

func squash(s string) string {
	return strings.NewReplacer("_", "", ".", "", "-", "").Replace(strings.ToLower(s))
}

func envName(key string) string {
	return envPrefix + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}
