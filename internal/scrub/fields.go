package scrub

import "strings"

func keyWords(key string) string {
	var b strings.Builder
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c == '_' || c == '-' || c == '.' {
			if b.Len() > 0 {
				b.WriteByte('/')
			}
			continue
		}
		if c >= 'A' && c <= 'Z' {
			if i > 0 && ((key[i-1] >= 'a' && key[i-1] <= 'z') || (i+1 < len(key) && key[i+1] >= 'a' && key[i+1] <= 'z' && key[i-1] >= 'A' && key[i-1] <= 'Z')) {
				b.WriteByte('/')
			}
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}

func fieldCategory(key string) string {
	w := keyWords(key)
	for _, suffix := range []string{"password", "secret", "token", "api/key", "access/key", "private/key", "access/key/id"} {
		if w == suffix || strings.HasSuffix(w, "/"+suffix) {
			return "secret"
		}
	}
	switch w {
	case "passwd", "pwd", "apikey", "accesskey", "authorization", "proxy/authorization", "cookie", "cookies", "set/cookie":
		return "secret"
	}
	if strings.HasPrefix(w, "http/request/header/") || strings.HasPrefix(w, "http/response/header/") {
		if strings.HasSuffix(w, "/authorization") || strings.HasSuffix(w, "/cookie") || strings.HasSuffix(w, "/cookies") {
			return "secret"
		}
		if strings.HasSuffix(w, "/requestid") {
			return "id"
		}
	}
	for _, suffix := range []string{"id", "uuid", "guid"} {
		if w == suffix || strings.HasSuffix(w, "/"+suffix) {
			return "id"
		}
	}
	switch w {
	case "requestid", "correlationid":
		return "id"
	case "host", "hostname", "host/name", "publisher/domain":
		return "hostname"
	}
	if strings.HasSuffix(w, "/name") {
		return "name"
	}
	switch w {
	case "name", "user", "username", "organisation", "organization", "workspace", "project", "tenant", "account":
		return "name"
	}
	return ""
}

func preservedField(key string) bool {
	switch key {
	case "tf_req_id", "tf_req_duration_ms", "tf_rpc", "tf_resource_type", "tf_data_source_type", "tf_provider_addr":
		return true
	}
	return false
}
