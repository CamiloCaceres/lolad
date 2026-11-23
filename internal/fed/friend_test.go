package fed

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func sampleCode(t testing.TB) friendCode {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	sec := make([]byte, 16)
	rand.Read(sec)
	return friendCode{T: "ana", D: "Ana", N: "ana", K: base64.RawURLEncoding.EncodeToString(pub), S: base64.RawURLEncoding.EncodeToString(sec),
		H: "ana", A: [][2]string{{"maple", "Maple"}, {"owl", "Owl"}}, E: 1795999999}
}

func TestFriendLinkCodec(t *testing.T) {
	c := sampleCode(t)
	link := encodeFriendLink("https://lolad.caceresdev.com/join", c)
	if len(link) > 400 {
		t.Fatal("typical link too long:", len(link))
	}
	frag := link[strings.IndexByte(link, '#'):]
	for _, in := range []string{link, frag, frag[1:], "Here: " + link + "  ", strings.Replace(link, "#2.", "#2.\n", 1), link[:60] + "\n" + link[60:]} {
		got, e := parseFriendLink(in)
		if e != nil {
			t.Fatalf("%q: %v", in, e)
		}
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(c)
		if string(a) != string(b) {
			t.Fatalf("round trip\n%s\n%s", a, b)
		}
	}
	if c.provider() != "ana.lolad.local" || fingerprint(c.publicKey()) == "" {
		t.Fatal("key or provider")
	}
	if u, hub, ok := c.target("https://hub.example.com"); !ok || u != "https://hub.example.com/p/ana" || hub != "https://hub.example.com" {
		t.Fatal(u, hub, ok)
	}
	if _, _, ok := c.target(""); ok {
		t.Fatal("hub link resolved with no default hub")
	}
	c.HU = "https://other.example.com"
	if u, _, _ := c.target("https://hub.example.com"); u != "https://other.example.com/p/ana" {
		t.Fatal(u)
	}
}

func TestFriendLinkRejects(t *testing.T) {
	mutate := func(f func(*friendCode)) string {
		c := sampleCode(t)
		f(&c)
		return encodeFriendLink("https://x/join", c)
	}
	bad := map[string]string{
		"empty":           "",
		"v1 prefix":       "lolad-invite:abc",
		"no version":      "https://x/join#abc",
		"not base64":      "https://x/join#2.!!!",
		"not json":        "https://x/join#2." + base64.RawURLEncoding.EncodeToString([]byte("nope")),
		"unknown field":   "https://x/join#2." + base64.RawURLEncoding.EncodeToString([]byte(`{"t":"ana","zz":1}`)),
		"short key":       mutate(func(c *friendCode) { c.K = "AAAA" }),
		"short secret":    mutate(func(c *friendCode) { c.S = "AAAA" }),
		"bad tenant":      mutate(func(c *friendCode) { c.T = "Ana!" }),
		"bad user":        mutate(func(c *friendCode) { c.N = "" }),
		"no expiry":       mutate(func(c *friendCode) { c.E = 0 }),
		"no way to reach": mutate(func(c *friendCode) { c.H = "" }),
		"two ways":        mutate(func(c *friendCode) { c.U = "https://ana.example.com" }),
		"bad handle":      mutate(func(c *friendCode) { c.H = "-x" }),
		"hub with path":   mutate(func(c *friendCode) { c.HU = "https://hub.example.com/evil" }),
		"http hub":        mutate(func(c *friendCode) { c.HU = "http://hub.example.com" }),
		"bad agent":       mutate(func(c *friendCode) { c.A = [][2]string{{"Bad Name", ""}} }),
		"too many agents": mutate(func(c *friendCode) {
			c.A = make([][2]string, 9)
			for i := range c.A {
				c.A[i] = [2]string{"a", ""}
			}
		}),
		"too long overall": "https://x/join#2." + strings.Repeat("A", maxFriendInput),
	}
	for name, in := range bad {
		if _, e := parseFriendLink(in); e == nil || !strings.Contains(e.Error(), "invite link") {
			t.Errorf("%s: accepted (%v)", name, e)
		}
	}
}

// The largest link CreateFriendInvite can make stays under the cap (it
// drops agent display names first when needed).
func TestFriendLinkWorstCaseSize(t *testing.T) {
	c := sampleCode(t)
	c.T, c.N, c.D, c.H = strings.Repeat("t", 32), strings.Repeat("n", 32), strings.Repeat("D", 64), strings.Repeat("h", 32)
	c.HU = "https://" + strings.Repeat("q", 52)
	c.A = nil
	for i := 0; i < maxOfferedAgents; i++ {
		c.A = append(c.A, [2]string{strings.Repeat(string(rune('a'+i)), 63), strings.Repeat("D", 64)})
	}
	link := encodeFriendLink(defaultJoinURL, c)
	if len(link) <= maxFriendLink {
		t.Log("worst case fits with display names:", len(link))
	}
	for i := range c.A {
		c.A[i][1] = ""
	}
	if link = encodeFriendLink(defaultJoinURL, c); len(link) > maxFriendLink {
		t.Fatal("worst case without display names still too long:", len(link))
	}
	if _, e := parseFriendLink(link); e != nil {
		t.Fatal(e)
	}
}

func FuzzParseFriendLink(f *testing.F) {
	f.Add(encodeFriendLink("https://x/join", sampleCode(f)))
	f.Add("https://x/join#2.e30")
	f.Add("#2.")
	f.Fuzz(func(t *testing.T, s string) {
		c, e := parseFriendLink(s)
		if e != nil {
			return
		}
		if len(s) > maxFriendInput || !validProvider(c.provider()) || (c.H == "") == (c.U == "") || len(c.A) > maxOfferedAgents {
			t.Fatalf("accepted invalid link %q: %+v", s, c)
		}
	})
}
