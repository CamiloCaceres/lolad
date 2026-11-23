package policy

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// AccountDisplay turns an OS account (user.Current's Username and Name) into
// a name to show people: the account's full name when it has one, otherwise
// the login, title-cased ("usuario" -> "Usuario", "camilo.caceres" ->
// "Camilo Caceres").
//
// On Windows, Go asks GetUserNameEx(NameDisplay) for Name. A local account
// that is not in a domain has no display name there, and Go then falls back
// to the SAM name, "CAMILO\Usuario". That, and a Name that only repeats the
// login, count as no full name.
func AccountDisplay(username, fullName string) string {
	login := loginOf(username)
	full := strings.TrimSpace(fullName)
	if full != "" && !strings.ContainsAny(full, `\/`) && !strings.EqualFold(full, login) && !strings.EqualFold(full, username) {
		return full
	}
	if login == "" {
		login = loginOf(full)
	}
	return titleLogin(login)
}

// loginOf drops a DOMAIN\ or path prefix from an OS login.
func loginOf(username string) string {
	return strings.TrimSpace(username[strings.LastIndexAny(username, `\/`)+1:])
}

// titleLogin makes a login readable: separators become spaces and each word
// starts with a capital ("JSMITH" -> "Jsmith", "ana_maria" -> "Ana Maria").
func titleLogin(login string) string {
	words := strings.FieldsFunc(login, func(r rune) bool { return r == '.' || r == '_' || r == '-' || unicode.IsSpace(r) })
	for i, w := range words {
		r, n := utf8.DecodeRuneInString(w)
		words[i] = string(unicode.ToUpper(r)) + strings.ToLower(w[n:])
	}
	return strings.Join(words, " ")
}

// fixStoredDisplay repairs a display name that an earlier beta wrote to
// org.yaml on Windows in the "DOMAIN\user" form, so friends never see it. A
// name the person typed (anything with a space, or without a backslash) is
// kept as it is.
func fixStoredDisplay(d string) string {
	if !strings.Contains(d, `\`) || strings.ContainsAny(d, " \t") {
		return d
	}
	if fixed := titleLogin(loginOf(d)); fixed != "" {
		return fixed
	}
	return d
}
