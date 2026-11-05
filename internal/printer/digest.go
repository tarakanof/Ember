package printer

import (
	"crypto/md5"
	"encoding/hex"
	"strings"
)

type challenge struct {
	realm string
	nonce string
	stale bool
}

func parseChallenge(h string) (challenge, bool) {
	scheme, rest, _ := strings.Cut(strings.TrimSpace(h), " ")
	if !strings.EqualFold(scheme, "Digest") {
		return challenge{}, false
	}
	params := parseAuthParams(rest)
	c := challenge{realm: params["realm"], nonce: params["nonce"], stale: strings.EqualFold(params["stale"], "true")}
	if c.nonce == "" {
		return challenge{}, false
	}
	return c, true
}

func parseAuthParams(s string) map[string]string {
	out := map[string]string{}
	for {
		s = strings.TrimLeft(s, " \t,")
		if s == "" {
			return out
		}
		key, rest, ok := strings.Cut(s, "=")
		if !ok {
			return out
		}
		key = strings.ToLower(strings.TrimSpace(key))
		rest = strings.TrimLeft(rest, " \t")
		var val string
		if strings.HasPrefix(rest, `"`) {
			end := 1
			var b strings.Builder
			for end < len(rest) && rest[end] != '"' {
				if rest[end] == '\\' && end+1 < len(rest) {
					end++
				}
				b.WriteByte(rest[end])
				end++
			}
			val = b.String()
			if end < len(rest) {
				end++
			}
			s = rest[end:]
		} else {
			v, tail, _ := strings.Cut(rest, ",")
			val, s = strings.TrimSpace(v), tail
		}
		out[key] = val
	}
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func digestResponse(user, password, realm, nonce, method, uri string) string {
	ha1 := md5hex(user + ":" + realm + ":" + password)
	ha2 := md5hex(method + ":" + uri)
	return md5hex(ha1 + ":" + nonce + ":" + ha2)
}

func authorization(user, password string, c challenge, method, uri string) string {
	return `Digest username="` + quote(user) + `", realm="` + quote(c.realm) + `", nonce="` + quote(c.nonce) +
		`", uri="` + quote(uri) + `", response="` + digestResponse(user, password, c.realm, c.nonce, method, uri) + `"`
}

func quote(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}
