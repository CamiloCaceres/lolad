package fed

import (
	"strings"
	"testing"

	"github.com/CamiloCaceres/lolad/internal/core"
	"github.com/CamiloCaceres/lolad/internal/deliver"
)

// TestFriendChatsWithSharedAgent is the whole friend conversation across two
// lolads: Jugui's human writes to Milo's shared agent from the chat window,
// the agent's notice says who that is, and the agent's `lolad msg reply`
// lands in Jugui's conversation with the agent, in the same thread. Run with a
// pre-approved friend (an invite link) and with a share that needs approval.
func TestFriendChatsWithSharedAgent(t *testing.T) {
	for _, invited := range []bool{true, false} {
		t.Run(map[bool]string{true: "invited", false: "approved"}[invited], func(t *testing.T) {
			milo, jugui := setup(t, "milo"), setup(t, "jugui")
			milo.run(t)
			jugui.run(t)
			pair(t, milo, jugui)
			friend, agent := "jugui@"+jugui.p.Name(), "agent@"+milo.p.Name()
			if invited {
				share(t, milo, jugui, friend)
			} else {
				share(t, milo, jugui)
			}
			// Jugui's lolad lists its human to Milo's (friend links do this).
			reach := true
			if _, e := jugui.f.UpdateConnection(t.Context(), milo.p.Name(), core.ConnectionUpdate{UserReachable: &reach}); e != nil {
				t.Fatal(e)
			}

			// The chat window's send (web/inbox.go).
			sent, e := jugui.p.Send(t.Context(), "jugui", core.Outgoing{To: agent, Subject: "Can you help with my taxes?", Body: "Hi! Milo said you could help.", Type: "request",
				Context: map[string]any{"userchat": map[string]any{"kind": "say"}}})
			if e != nil {
				t.Fatal(e)
			}
			if !invited {
				delivery(t, jugui, sent.ID, core.DeliveryHeld)
				if _, e := milo.f.DecideGrant(t.Context(), jugui.p.Name(), friend, "agent", true); e != nil {
					t.Fatal(e)
				}
			}
			delivery(t, jugui, sent.ID, core.DeliveryDelivered)
			in, e := milo.p.Get(t.Context(), sent.ID)
			if e != nil || in.Remote != jugui.p.Name() || in.ToName != "agent" || in.Status != core.MailUnread {
				t.Fatalf("agent's copy: %+v %v", in, e)
			}

			// The agent's notice names a person on Jugui's lolad, not its user.
			s, ok := milo.f.Sender(t.Context(), in.FromName)
			if !ok || s.Kind != "human" || s.Display != "Jugui" || s.PeerDisplay != "Jugui" {
				t.Fatalf("sender: %+v %v", s, ok)
			}
			notice := deliver.SenderNotice(in, s)
			if !strings.Contains(notice, "external from Jugui (jugui@jugui.lolad.local), a person on Jugui's lolad, not your user: Can you help with my taxes?") || !strings.HasSuffix(notice, "then answer with lolad msg reply") {
				t.Fatal(notice)
			}

			// `lolad msg reply <id>`: to the envelope's sender, in reply to it.
			rep, e := milo.p.Send(t.Context(), "agent", core.Outgoing{To: friend, Subject: "Re: " + in.Subject, Body: "Sure, send me the forms.", Type: "response", InReplyTo: in.ID})
			if e != nil {
				t.Fatalf("the agent's reply was refused: %v", e)
			}
			delivery(t, milo, rep.ID, core.DeliveryDelivered)
			thread, e := jugui.p.List(t.Context(), "jugui", core.MailFilter{Box: "all", Peer: agent, Limit: 10})
			if e != nil {
				t.Fatal(e)
			}
			if len(thread) != 2 {
				t.Fatalf("Jugui's conversation with the agent has %d messages, want 2", len(thread))
			}
			for _, m := range thread {
				if m.ThreadID != sent.ThreadID {
					t.Fatalf("message %s is in thread %s, want %s", m.ID, m.ThreadID, sent.ThreadID)
				}
				if m.FromName == agent && (m.Body != "Sure, send me the forms." || m.InReplyTo != sent.ID || m.Status != core.MailUnread) {
					t.Fatalf("reply as Jugui sees it: %+v", m)
				}
			}
		})
	}
}

// TestSenderWithoutDirectoryEntry: a participant its lolad doesn't list (here
// Jugui's human, not advertised) still gets its lolad's name; Kind stays
// unknown rather than guessed. Unconnected lolads aren't described.
func TestSenderWithoutDirectoryEntry(t *testing.T) {
	milo, jugui := setup(t, "milo"), setup(t, "jugui")
	pair(t, milo, jugui)
	s, ok := milo.f.Sender(t.Context(), "jugui@"+jugui.p.Name())
	if !ok || s.Kind != "" || s.PeerDisplay != "Jugui" || s.Peer != jugui.p.Name() {
		t.Fatalf("sender: %+v %v", s, ok)
	}
	if !strings.Contains(deliver.ExternalSender(s.Address, s.Peer, s), "jugui@jugui.lolad.local, on Jugui's lolad") {
		t.Fatal(deliver.ExternalSender(s.Address, s.Peer, s))
	}
	if _, ok := milo.f.Sender(t.Context(), "ana@studio.lolad.local"); ok {
		t.Fatal("a lolad we're not connected to was described")
	}
}
