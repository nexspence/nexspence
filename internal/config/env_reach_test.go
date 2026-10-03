package config

import (
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// leafKeys lists every scalar (string or int) setting of t as its dotted
// viper key, with the field index path to read it back.
func leafKeys(t reflect.Type, prefix string, index []int, out map[string][]int) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("mapstructure"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		key := tag
		if prefix != "" {
			key = prefix + "." + tag
		}
		idx := append(append([]int{}, index...), i)
		switch f.Type.Kind() {
		case reflect.Struct:
			if f.Type.PkgPath() == "time" {
				continue
			}
			leafKeys(f.Type, key, idx, out)
		case reflect.String, reflect.Int:
			out[key] = idx
		}
	}
}

// Viper's AutomaticEnv + Unmarshal silently drops keys that have neither a
// default nor a config-file value: such a setting cannot be configured from
// the environment at all (it was why ldap.host, ldap.bind_dn,
// ldap.bind_password and ldap.search_base were unreachable). Every scalar
// setting must reach the Config from NEXSPENCE_<KEY> with no config file.
func TestLoad_EveryScalarSettingIsReachableFromEnv(t *testing.T) {
	keys := map[string][]int{}
	leafKeys(reflect.TypeOf(Config{}), "", nil, keys)
	if len(keys) < 50 {
		t.Fatalf("found only %d settings — walker broken?", len(keys))
	}
	missing := filepath.Join(t.TempDir(), "absent.yaml")
	for key, idx := range keys {
		t.Run(key, func(t *testing.T) {
			env := "NEXSPENCE_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
			field := reflect.TypeOf(Config{}).FieldByIndex(idx)
			want := "env-" + strings.ReplaceAll(key, ".", "-")
			if field.Type.Kind() == reflect.Int {
				want = "7"
			}
			// The minimum Load requires, unless it is the key under test.
			for k, v := range map[string]string{
				"NEXSPENCE_DATABASE_DSN":    "postgres://u:p@localhost/db",
				"NEXSPENCE_AUTH_JWT_SECRET": "env-reach-test-secret-0123456789abcdef",
			} {
				if k != env {
					t.Setenv(k, v)
				}
			}
			t.Setenv(env, want)
			cfg, err := Load(missing)
			if err != nil {
				if validated[key] {
					t.Skipf("validated setting, the sentinel is refused: %v", err)
				}
				t.Fatalf("Load with %s=%q: %v", env, want, err)
			}
			got := reflect.ValueOf(*cfg).FieldByIndex(idx)
			if fmtVal(got) != want {
				t.Errorf("%s=%q did not reach %s (got %q)", env, want, key, fmtVal(got))
			}
		})
	}
}

// validated are settings whose sentinel value Load rightly refuses.
var validated = map[string]bool{
	"auth.encryption_key":  true, // base64 of 32 bytes
	"auth.jwt_secret":      true, // at least 32 characters
	"storage.default_type": true, // local, s3 or azure
}

func fmtVal(v reflect.Value) string {
	if v.Kind() == reflect.Int {
		return strconv.FormatInt(v.Int(), 10)
	}
	return v.String()
}
