package fed

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/core"
)

// Friend connect (docs/friend-connect-design.md §2, §3): one link that pairs
// two lolads, joins the hub when needed, and pre-approves each side's human
// and offered agents. core.FriendInviter is implemented here.

const (
	friendTTL        = 7 * 24 * time.Hour
	maxFriendLink    = 1500 // longest link CreateFriendInvite makes
	maxFriendInput   = 4096 // longest input Preview/Accept read
	maxOfferedAgents = 8
	pendingRetry     = 10 * time.Minute
	defaultJoinURL   = "https://lolad.caceresdev.com/join"
)

// HubMember is this lolad's hub membership, implemented by the app.
type HubMember interface {
	// Membership reports the hub this lolad is on, if any.
	Membership() (hubURL, handle string, ok bool)
	// Join signs up on hubURL with no code (open sign-up), preferring
	// preferredHandle, and starts the tunnel. It returns the member URL.
	// Refusals implement UserMessage() string.
	Join(ctx context.Context, hubURL, preferredHandle string) (memberURL string, err error)
}

// SetHub wires the hub membership and the defaults friend links use: the
// built-in hub ("" turns auto-join off) and the base of invite links.
func (f *Engine) SetHub(h HubMember, defaultHub, joinURL string) {
	f.urlMu.Lock()
	defer f.urlMu.Unlock()
	f.hubMember = h
	f.defaultHub = strings.TrimRight(defaultHub, "/")
	f.joinURL = strings.TrimRight(joinURL, "/")
	if f.joinURL == "" {
		f.joinURL = defaultJoinURL
	}
}

func (f *Engine) hubConfig() (HubMember, string, string) {
	f.urlMu.RLock()
	defer f.urlMu.RUnlock()
	j := f.joinURL
	if j == "" {
		j = defaultJoinURL
	}
	return f.hubMember, f.defaultHub, j
}

// membership is the hub this lolad is on; ok false without one or without a
// HubMember.
func (f *Engine) membership() (string, string, bool) {
	h, _, _ := f.hubConfig()
	if h == nil {
		return "", "", false
	}
	return h.Membership()
}

// friendError is a refusal written for people.
type friendError struct {
	kind error
	msg  string
}

func (e friendError) Error() string       { return e.kind.Error() + ": " + e.msg }
func (e friendError) Unwrap() error       { return e.kind }
func (e friendError) UserMessage() string { return e.msg }

func refuse(kind error, format string, a ...any) error {
	return friendError{kind, fmt.Sprintf(format, a...)}
}

// userMessage keeps an error that already speaks to people, else uses msg.
func userMessage(e error, kind error, msg string) error {
	var um interface{ UserMessage() string }
	if errors.As(e, &um) {
		return e
	}
	return friendError{kind, msg}
}

// ---------------------------------------------------------------- link codec

// friendCode is the v2 link payload, JSON with short keys (design §2.2).
type friendCode struct {
	T  string      `json:"t"`           // inviter tenant
	D  string      `json:"d,omitempty"` // inviter display name (a hint)
	N  string      `json:"n"`           // inviter human's local name
	K  string      `json:"k"`           // inviter provider key, raw Ed25519, base64url
	S  string      `json:"s"`           // pairing secret, 16 bytes, base64url
	H  string      `json:"h,omitempty"` // handle on a hub
	HU string      `json:"H,omitempty"` // that hub, when not the default
	U  string      `json:"u,omitempty"` // direct URL when not on a hub
	A  [][2]string `json:"a,omitempty"` // offered agents: [slug, display]
	E  int64       `json:"e"`           // expiry, unix seconds
}

var (
	slugRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	handleRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,30}[a-z0-9])$`)
)

func (c friendCode) provider() string { return c.T + ".lolad.local" }

func (c friendCode) publicKey() string {
	k, _ := base64.RawURLEncoding.DecodeString(c.K)
	return pubPEM(ed25519.PublicKey(k))
}

func (c friendCode) display() string {
	if c.D != "" {
		return c.D
	}
	return c.T
}

func (c friendCode) offered() []string {
	out := make([]string, 0, len(c.A))
	for _, a := range c.A {
		out = append(out, a[0])
	}
	return out
}

// target is where to pair: the member URL on its hub, or the direct URL.
// hubURL is set when the inviter is on a hub.
func (c friendCode) target(defaultHub string) (url, hubURL string, ok bool) {
	if c.H == "" {
		return c.U, "", c.U != ""
	}
	hubURL = c.HU
	if hubURL == "" {
		hubURL = defaultHub
	}
	if hubURL == "" {
		return "", "", false
	}
	return hubURL + "/p/" + c.H, hubURL, true
}

func encodeFriendLink(base string, c friendCode) string {
	raw, _ := json.Marshal(c)
	return base + "#2." + base64.RawURLEncoding.EncodeToString(raw)
}

var errBadLink = refuse(core.ErrInvalid, "That doesn't look like a Lolad invite link. Copy the whole link and paste it again.")

// parseFriendLink reads a v2 link, the part after '#', or either with the
// spaces and line breaks chat apps add.
func parseFriendLink(s string) (friendCode, error) {
	if len(s) > maxFriendInput {
		return friendCode{}, errBadLink
	}
	s = strings.Join(strings.Fields(s), "")
	if i := strings.LastIndexByte(s, '#'); i >= 0 {
		s = s[i+1:]
	}
	body, ok := strings.CutPrefix(s, "2.")
	if !ok {
		return friendCode{}, errBadLink
	}
	raw, e := base64.RawURLEncoding.DecodeString(strings.TrimRight(body, "="))
	if e != nil {
		return friendCode{}, errBadLink
	}
	var c friendCode
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil {
		return friendCode{}, errBadLink
	}
	key, e1 := base64.RawURLEncoding.DecodeString(c.K)
	sec, e2 := base64.RawURLEncoding.DecodeString(c.S)
	switch {
	case e1 != nil || len(key) != ed25519.PublicKeySize, e2 != nil || len(sec) != 16,
		!validProvider(c.provider()), !slugRE.MatchString(c.N), c.E <= 0,
		len(c.D) > 64, len(c.A) > maxOfferedAgents,
		(c.H == "") == (c.U == ""), // exactly one way to reach the inviter
		c.H != "" && !handleRE.MatchString(c.H),
		c.HU != "" && (c.H == "" || !validURL(c.HU) || strings.ContainsAny(strings.TrimPrefix(strings.TrimPrefix(c.HU, "https://"), "http://"), "/?#")),
		c.U != "" && !validURL(c.U):
		return friendCode{}, errBadLink
	}
	for _, a := range c.A {
		if !slugRE.MatchString(a[0]) || len(a[1]) > 64 {
			return friendCode{}, errBadLink
		}
	}
	return c, nil
}

// ---------------------------------------------------------------- create

func (f *Engine) tenant() string { return strings.TrimSuffix(f.o.Provider.Name(), ".lolad.local") }

// offer checks that agents are ours, drops duplicates and caps the list.
func (f *Engine) offer(agents []string) ([][2]string, error) {
	byName := map[string]core.Agent{}
	for _, a := range f.o.Agents() {
		byName[a.Name] = a
	}
	seen := map[string]bool{}
	var out [][2]string
	for _, n := range agents {
		a, ok := byName[n]
		if !ok {
			return nil, refuse(core.ErrInvalid, "There is no assistant called %q here.", n)
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, [2]string{a.Name, a.Display})
	}
	if len(out) > maxOfferedAgents {
		return nil, refuse(core.ErrInvalid, "Offer at most %d assistants in one invite.", maxOfferedAgents)
	}
	return out, nil
}

// reachable makes sure peers can reach this lolad, joining hubURL (or the
// default hub) when it is on no hub and has no direct URL. preferHub joins
// even with a direct URL: a hub member can rarely reach a tailnet address.
func (f *Engine) ensureReachable(ctx context.Context, hubURL string, preferHub bool) error {
	if _, _, on := f.membership(); on {
		return nil
	}
	if f.selfURL() != "" && !preferHub {
		return nil
	}
	h, defaultHub, _ := f.hubConfig()
	if hubURL == "" {
		hubURL = defaultHub
	}
	if h == nil || hubURL == "" {
		if f.selfURL() != "" {
			return nil
		}
		return refuse(core.ErrInvalid, "Lolad can't be reached by friends yet: joining the Lolad network is turned off in this Lolad's settings.")
	}
	if _, e := h.Join(ctx, hubURL, f.tenant()); e != nil {
		return userMessage(e, core.ErrInvalid, "Lolad couldn't reach its network. Check your internet connection and try again.")
	}
	return nil
}

// CreateFriendInvite makes a single-use link, valid 7 days, that connects
// whoever opens it with this lolad and pre-approves them (core.FriendInviter).
func (f *Engine) CreateFriendInvite(ctx context.Context, r core.FriendInviteRequest) (core.FriendInvite, error) {
	if !f.Enabled() {
		return core.FriendInvite{}, refuse(core.ErrDenied, "Connections are turned off in this Lolad's settings.")
	}
	offer, e := f.offer(r.Agents)
	if e != nil {
		return core.FriendInvite{}, e
	}
	note := strings.TrimSpace(r.For)
	if len(note) > 100 {
		note = note[:100]
	}
	if e = f.ensureReachable(ctx, "", false); e != nil {
		return core.FriendInvite{}, e
	}
	_, defaultHub, joinURL := f.hubConfig()
	secret := make([]byte, 16)
	if _, e = rand.Read(secret); e != nil {
		return core.FriendInvite{}, e
	}
	expires := f.o.Clock().UTC().Add(friendTTL).Truncate(time.Second)
	c := friendCode{T: f.tenant(), D: f.o.Display, N: f.o.Org().User.Name, S: base64.RawURLEncoding.EncodeToString(secret), A: offer, E: expires.Unix()}
	c.K = base64.RawURLEncoding.EncodeToString(f.key.Public().(ed25519.PublicKey))
	if hubURL, handle, on := f.membership(); on {
		c.H = handle
		if hubURL != defaultHub {
			c.HU = hubURL
		}
	} else if c.U = f.selfURL(); c.U == "" {
		return core.FriendInvite{}, refuse(core.ErrInvalid, "Lolad can't be reached by friends yet.")
	}
	if len(c.D) > 64 {
		c.D = c.D[:64]
	}
	link := encodeFriendLink(joinURL, c)
	if len(link) > maxFriendLink { // drop agent display names first
		for i := range c.A {
			c.A[i][1] = ""
		}
		if link = encodeFriendLink(joinURL, c); len(link) > maxFriendLink {
			return core.FriendInvite{}, refuse(core.ErrInvalid, "That invite would be too long to send. Offer fewer assistants.")
		}
	}
	slugs, _ := json.Marshal(c.offered())
	hash := hashSecret(c.S)
	if _, e = f.o.DB.ExecContext(ctx, `INSERT INTO fed_invites(secret_hash,expires,kind,offer,for_note,created) VALUES(?,?,'friend',?,?,?)`,
		hash, expires.Unix(), string(slugs), note, f.o.Clock().Unix()); e != nil {
		return core.FriendInvite{}, e
	}
	return core.FriendInvite{ID: hash[:16], Link: link, ExpiresAt: expires}, nil
}

// FriendInvites lists this lolad's friend links, newest first, never codes.
func (f *Engine) FriendInvites(ctx context.Context) ([]core.FriendInviteInfo, error) {
	rows, e := f.o.DB.QueryContext(ctx, `SELECT secret_hash,expires,COALESCE(used_at,0),COALESCE(used_by,''),COALESCE(offer,'[]'),COALESCE(for_note,''),COALESCE(created,0) FROM fed_invites WHERE kind='friend' ORDER BY created DESC`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []core.FriendInviteInfo{}
	for rows.Next() {
		var hash, usedBy, offer, note string
		var expires, used, created int64
		if e = rows.Scan(&hash, &expires, &used, &usedBy, &offer, &note, &created); e != nil {
			return nil, e
		}
		v := core.FriendInviteInfo{ID: hash[:16], For: note, Agents: []string{}, CreatedAt: time.Unix(created, 0).UTC(), ExpiresAt: time.Unix(expires, 0).UTC()}
		_ = json.Unmarshal([]byte(offer), &v.Agents)
		if used > 0 {
			v.UsedAt = time.Unix(used, 0).UTC()
			v.UsedByDisplay = usedBy
			f.mu.Lock()
			if p := f.find(usedBy); p != nil && p.Display != "" {
				v.UsedByDisplay = p.Display
			}
			f.mu.Unlock()
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CancelFriendInvite deletes a link nobody has used; it stops working.
func (f *Engine) CancelFriendInvite(ctx context.Context, id string) error {
	if len(id) != 16 {
		return core.ErrNotFound
	}
	res, e := f.o.DB.ExecContext(ctx, `DELETE FROM fed_invites WHERE kind='friend' AND substr(secret_hash,1,16)=? AND used_at IS NULL`, id)
	if e != nil {
		return e
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return core.ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------- preview and accept

// PreviewFriendInvite reads a link with no network and no side effects.
func (f *Engine) PreviewFriendInvite(ctx context.Context, link string) (core.InvitePreview, error) {
	if v1, ok := strings.CutPrefix(strings.TrimSpace(link), "lolad-invite:"); ok {
		c, e := decodeV1(v1)
		if e != nil {
			return core.InvitePreview{}, errBadLink
		}
		return core.InvitePreview{FromDisplay: c.Display, Agents: []core.AgentOffer{}, SameName: c.Provider == f.o.Provider.Name(), AlreadyConnected: f.connectedWith(c.Provider, c.PublicKey)}, nil
	}
	c, e := parseFriendLink(link)
	if e != nil {
		return core.InvitePreview{}, e
	}
	p := core.InvitePreview{FromDisplay: c.display(), Agents: []core.AgentOffer{}, ExpiresAt: time.Unix(c.E, 0).UTC(),
		Expired: f.o.Clock().Unix() >= c.E, SameName: c.provider() == f.o.Provider.Name(), AlreadyConnected: f.connectedWith(c.provider(), c.publicKey())}
	for _, a := range c.A {
		d := a[1]
		if d == "" {
			d = a[0]
		}
		p.Agents = append(p.Agents, core.AgentOffer{Slug: a[0], Display: d})
	}
	if c.H != "" {
		_, _, on := f.membership()
		p.JoinsNetwork = !on
	}
	return p, nil
}

func decodeV1(s string) (inviteCode, error) {
	var c inviteCode
	raw, e := base64.RawURLEncoding.DecodeString(s)
	if e != nil || json.Unmarshal(raw, &c) != nil || c.V != 1 {
		return c, errBadLink
	}
	return c, nil
}

func (f *Engine) connectedWith(provider, key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.find(provider)
	return p != nil && !p.Removed && fingerprint(p.PublicKey) == fingerprint(key)
}

// AcceptFriendInvite connects with the link's lolad in one call: it joins the
// hub if needed, pairs, and pre-approves both sides' humans and offered
// agents. When the inviter is offline it returns FriendWaiting and finishes
// on its own once the inviter is back (until the link expires).
func (f *Engine) AcceptFriendInvite(ctx context.Context, link string, r core.AcceptRequest) (core.AcceptResult, error) {
	if !f.Enabled() {
		return core.AcceptResult{}, refuse(core.ErrDenied, "Connections are turned off in this Lolad's settings.")
	}
	if strings.HasPrefix(strings.TrimSpace(link), "lolad-invite:") {
		conn, e := f.AcceptInvite(ctx, link)
		if e != nil {
			return core.AcceptResult{}, userMessage(e, core.ErrInvalid, "Could not accept that invitation. Ask for a new invitation and try again.")
		}
		return core.AcceptResult{Connection: conn, State: core.FriendConnected}, nil
	}
	// 1. Local checks, no side effects.
	c, e := parseFriendLink(link)
	if e != nil {
		return core.AcceptResult{}, e
	}
	if f.o.Clock().Unix() >= c.E {
		return core.AcceptResult{}, refuse(core.ErrInvalid, "This invite has expired. Ask %s for a new link.", c.display())
	}
	if c.provider() == f.o.Provider.Name() {
		return core.AcceptResult{}, sameNameError{c.provider()}
	}
	f.mu.Lock()
	existing := f.find(c.provider())
	mismatch := existing != nil && fingerprint(existing.PublicKey) != fingerprint(c.publicKey())
	f.mu.Unlock()
	if mismatch {
		return core.AcceptResult{}, refuse(core.ErrDenied, "This invite comes from a different Lolad than the %s you are already connected with. Ask them which link is theirs.", c.display())
	}
	offer, e := f.offer(r.Agents)
	if e != nil {
		return core.AcceptResult{}, e
	}
	_, defaultHub, _ := f.hubConfig()
	url, hubURL, ok := c.target(defaultHub)
	if !ok {
		return core.AcceptResult{}, refuse(core.ErrInvalid, "This invite needs the Lolad network, which is turned off in this Lolad's settings.")
	}
	// 2. Be reachable: join the inviter's hub when on none.
	if e = f.ensureReachable(ctx, hubURL, hubURL != ""); e != nil {
		return core.AcceptResult{}, e
	}
	mine := make([]string, len(offer))
	for i, a := range offer {
		mine[i] = a[0]
	}
	// 3. Pair, or wait for the inviter.
	return f.finishFriend(ctx, c, url, mine, true)
}

// finishFriend pairs with the inviter and applies the pre-approvals. When
// the inviter is offline it records a pending accept (if record) and
// reports FriendWaiting.
func (f *Engine) finishFriend(ctx context.Context, c friendCode, url string, mine []string, record bool) (core.AcceptResult, error) {
	target := peer{Connection: core.Connection{Provider: c.provider(), Display: c.display(), URL: url}, PublicKey: c.publicKey()}
	conn, status, e := f.pairWith(ctx, target, c.S, f.o.Org().User.Name, mine)
	if e != nil {
		if status == 0 || status == 502 || status == 503 || status == 504 {
			if record {
				if pe := f.savePending(ctx, c, url, mine); pe != nil {
					return core.AcceptResult{}, pe
				}
				f.friendEvent("friend_waiting", "Waiting for "+c.display()+"'s Lolad to come online to finish connecting")
			}
			return core.AcceptResult{Connection: target.Connection, State: core.FriendWaiting,
				Message: fmt.Sprintf("Waiting for %s's Lolad to come online. You'll be connected automatically.", c.display())}, nil
		}
		if status == 403 {
			return core.AcceptResult{}, refuse(core.ErrDenied, "This invite was already used or is no longer valid. If that wasn't you, tell %s.", c.display())
		}
		return core.AcceptResult{}, userMessage(e, core.ErrInvalid, "Could not connect with "+c.display()+". Ask for a new link and try again.")
	}
	f.mu.Lock()
	e = f.applyFriendLocked(conn.Provider, c.N, c.offered(), mine)
	f.mu.Unlock()
	if e != nil {
		return core.AcceptResult{}, e
	}
	f.friendEvent("friend_connected", "Connected with "+conn.Display)
	f.mu.Lock()
	if p := f.find(conn.Provider); p != nil {
		conn = cloneConnection(p.Connection)
	}
	f.mu.Unlock()
	return core.AcceptResult{Connection: conn, State: core.FriendConnected}, nil
}

// applyFriendLocked lets the other side's human and offered agents reach
// ours without approval: the peer may reach the user, our offered agents
// are shared, and every pair across the two sides is granted (origin
// "invited"). A pair someone blocked stays blocked. f.mu must be held.
func (f *Engine) applyFriendLocked(provider, remoteUser string, remoteOffer, mine []string) error {
	p := f.find(provider)
	if p == nil {
		return core.ErrNotFound
	}
	remotes := []string{}
	if slugRE.MatchString(remoteUser) {
		remotes = append(remotes, remoteUser+"@"+provider)
	}
	for i, a := range remoteOffer {
		if i < maxOfferedAgents && slugRE.MatchString(a) {
			remotes = append(remotes, a+"@"+provider)
		}
	}
	locals := append([]string{f.o.Org().User.Name}, mine...)
	now := f.o.Clock().UTC()
	return f.mutate(func() error {
		p.UserReachable = true
		for _, a := range mine {
			shared := false
			for _, s := range p.Shares {
				shared = shared || s.Local == a
			}
			if !shared {
				p.Shares = append(p.Shares, core.Share{Local: a})
			}
		}
		for _, r := range remotes {
			for _, l := range locals {
				if g := f.grant(provider, r, l); g == nil {
					f.doc.Grants = append(f.doc.Grants, core.Grant{Peer: provider, Remote: r, Local: l, State: core.GrantAllowed, Origin: "invited", CreatedAt: now, DecidedAt: now})
				} else if g.State != core.GrantBlocked {
					g.State, g.Origin, g.DecidedAt = core.GrantAllowed, "invited", now
				}
			}
		}
		return nil
	})
}

func (f *Engine) friendEvent(kind, detail string) {
	if f.o.EventSink != nil {
		f.o.EventSink(core.Event{Kind: kind, Detail: detail})
	}
}

// friendInvite is the stored side of a friend link, read when it is used.
type friendInvite struct {
	friend bool
	offer  []string
	note   string
}

func (f *Engine) friendInviteRow(ctx context.Context, hash string) friendInvite {
	var kind, offer, note string
	if f.o.DB.QueryRowContext(ctx, `SELECT COALESCE(kind,''),COALESCE(offer,'[]'),COALESCE(for_note,'') FROM fed_invites WHERE secret_hash=?`, hash).Scan(&kind, &offer, &note) != nil || kind != "friend" {
		return friendInvite{}
	}
	v := friendInvite{friend: true, note: note}
	_ = json.Unmarshal([]byte(offer), &v.offer)
	return v
}

// friendPaired applies the inviter's side when a friend link is used: the
// mirror of what the accepter applied. f.mu must be held.
func (f *Engine) friendPairedLocked(inv friendInvite, b pairBody) {
	if !inv.friend {
		return
	}
	if e := f.applyFriendLocked(b.Provider, b.User, b.Offer, inv.offer); e != nil {
		go f.event("Cannot apply friend invite for " + b.Provider + ": " + e.Error())
		return
	}
	who := b.Display
	if who == "" {
		who = b.Provider
	}
	detail := who + " connected using your invite link"
	if inv.note != "" {
		detail += " (for " + inv.note + ")"
	}
	go f.friendEvent("friend_connected", detail)
}

// ---------------------------------------------------------------- pending accepts

func (f *Engine) savePending(ctx context.Context, c friendCode, url string, mine []string) error {
	raw, _ := json.Marshal(c)
	agents, _ := json.Marshal(mine)
	_, e := f.o.DB.ExecContext(ctx, `INSERT OR REPLACE INTO fed_pending(provider,code,url,agents,expires,created) VALUES(?,?,?,?,?,?)`,
		c.provider(), string(raw), url, string(agents), c.E, f.o.Clock().Unix())
	return e
}

func (f *Engine) pendingURLs() []string {
	rows, e := f.o.DB.Query(`SELECT url FROM fed_pending`)
	if e != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if rows.Scan(&u) == nil {
			out = append(out, u)
		}
	}
	return out
}

// retryPending tries pending accepts again: all of them, or those for url
// when the hub reports that peer online. Expired ones are dropped.
func (f *Engine) retryPending(ctx context.Context, url string) {
	f.pendingMu.Lock()
	defer f.pendingMu.Unlock()
	q, args := `SELECT provider,code,url,agents FROM fed_pending`, []any{}
	if url != "" {
		q, args = q+` WHERE url=?`, append(args, strings.TrimRight(url, "/"))
	}
	rows, e := f.o.DB.QueryContext(ctx, q, args...)
	if e != nil {
		return
	}
	type row struct{ provider, code, url, agents string }
	var list []row
	for rows.Next() {
		var v row
		if rows.Scan(&v.provider, &v.code, &v.url, &v.agents) == nil {
			list = append(list, v)
		}
	}
	rows.Close()
	for _, v := range list {
		var c friendCode
		var mine []string
		if json.Unmarshal([]byte(v.code), &c) != nil || json.Unmarshal([]byte(v.agents), &mine) != nil {
			f.dropPending(ctx, v.provider)
			continue
		}
		if f.o.Clock().Unix() >= c.E {
			f.dropPending(ctx, v.provider)
			f.friendEvent("friend_expired", c.display()+"'s Lolad wasn't online before the invite expired. Ask "+c.display()+" for a new link.")
			continue
		}
		res, e := f.finishFriend(ctx, c, v.url, mine, false)
		switch {
		case e != nil:
			f.dropPending(ctx, v.provider)
			f.friendEvent("friend_failed", userMessage(e, core.ErrInvalid, "Could not connect with "+c.display()+".").(interface{ UserMessage() string }).UserMessage())
		case res.State == core.FriendConnected:
			f.dropPending(ctx, v.provider)
		}
	}
}

func (f *Engine) dropPending(ctx context.Context, provider string) {
	_, _ = f.o.DB.ExecContext(ctx, `DELETE FROM fed_pending WHERE provider=?`, provider)
}

// migrateFriends adds the friend-link columns and the pending table.
func migrateFriends(db *sql.DB) error {
	if _, e := db.Exec(`CREATE TABLE IF NOT EXISTS fed_pending(provider TEXT PRIMARY KEY, code TEXT NOT NULL, url TEXT NOT NULL, agents TEXT NOT NULL DEFAULT '[]', expires INTEGER NOT NULL, created INTEGER NOT NULL)`); e != nil {
		return e
	}
	cols := map[string]bool{}
	rows, e := db.Query(`SELECT name FROM pragma_table_info('fed_invites')`)
	if e != nil {
		return e
	}
	for rows.Next() {
		var n string
		if e = rows.Scan(&n); e != nil {
			rows.Close()
			return e
		}
		cols[n] = true
	}
	rows.Close()
	for _, c := range []struct{ name, def string }{{"kind", "TEXT NOT NULL DEFAULT 'code'"}, {"offer", "TEXT"}, {"for_note", "TEXT"}, {"created", "INTEGER"}} {
		if !cols[c.name] {
			if _, e = db.Exec(`ALTER TABLE fed_invites ADD COLUMN ` + c.name + ` ` + c.def); e != nil {
				return e
			}
		}
	}
	return nil
}
