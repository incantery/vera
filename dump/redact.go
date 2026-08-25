package dump

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// A dump is for handing to someone. What it must not hand over is the
// contents of ~/.config/vera: API keys, the OTLP authorization header,
// the pairing secret. So every text file written into a dump passes
// through here first, and the redactor knows two things: the exact
// values it found in those files, and the shapes secrets usually take.

const redacted = "«redacted»"

type redactor struct {
	// values, longest first, so a secret that contains another is
	// replaced whole.
	values []string
	shapes []*regexp.Regexp
}

var secretShapes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization\s*[=:]\s*(?:basic|bearer)\s+)[A-Za-z0-9+/=_\-.]{12,}`),
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9+/=_\-.]{20,}`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{16,}`),
	regexp.MustCompile(`\bglc_[A-Za-z0-9+/=_\-]{16,}`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`(?i)("secret"\s*:\s*")[^"]+`),
}

// newRedactor learns the secrets: every value in the config files
// (KEY=VALUE files by value, anything else whole) and the identity's
// pairing secret.
func newRedactor(configDir, identityFile string) *redactor {
	r := &redactor{shapes: secretShapes}
	seen := map[string]bool{}
	add := func(v string) {
		v = strings.TrimSpace(strings.Trim(strings.TrimSpace(v), `"'`))
		if len(v) < 8 || seen[v] {
			return
		}
		seen[v] = true
		r.values = append(r.values, v)
	}
	if entries, err := os.ReadDir(configDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			b, err := os.ReadFile(filepath.Join(configDir, e.Name()))
			if err != nil {
				continue
			}
			text := string(b)
			if strings.HasSuffix(e.Name(), ".env") || strings.Contains(text, "=") {
				for _, line := range strings.Split(text, "\n") {
					line = strings.TrimPrefix(strings.TrimSpace(line), "export ")
					if _, v, ok := strings.Cut(line, "="); ok {
						add(v)
						// A header value like "Authorization=Basic abc"
						// is itself KEY=VALUE.
						if _, inner, ok := strings.Cut(v, "="); ok {
							add(inner)
						}
					}
				}
			} else {
				add(text)
			}
		}
	}
	if b, err := os.ReadFile(identityFile); err == nil {
		if m := regexp.MustCompile(`"secret"\s*:\s*"([^"]+)"`).FindStringSubmatch(string(b)); m != nil {
			add(m[1])
		}
	}
	sort.Slice(r.values, func(i, j int) bool { return len(r.values[i]) > len(r.values[j]) })
	return r
}

func (r *redactor) apply(text string) string {
	for _, v := range r.values {
		text = strings.ReplaceAll(text, v, redacted)
	}
	for _, re := range r.shapes {
		text = re.ReplaceAllStringFunc(text, func(m string) string {
			sub := re.FindStringSubmatch(m)
			if len(sub) > 1 && sub[1] != "" {
				return sub[1] + redacted
			}
			return redacted
		})
	}
	return text
}
