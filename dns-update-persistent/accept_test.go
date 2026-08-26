package dnsupdatepersist

import (
	"testing"

	"github.com/miekg/dns"
)

func TestMsgAcceptAllowsUpdate(t *testing.T) {
	var dh dns.Header
	dh.Bits = uint16(dns.OpcodeUpdate) << 11
	dh.Qdcount = 1
	dh.Ancount = 8
	dh.Nscount = 40
	dh.Arcount = 1
	if got := dns.DefaultMsgAcceptFunc(dh); got != dns.MsgAccept {
		t.Fatalf("UPDATE with large sections: got %v, want MsgAccept", got)
	}

	dh.Qdcount = 0
	if got := dns.DefaultMsgAcceptFunc(dh); got != dns.MsgReject {
		t.Fatalf("UPDATE with Qdcount=0: got %v, want MsgReject", got)
	}

	dh.Qdcount = 1
	dh.Bits |= 0x8000
	if got := dns.DefaultMsgAcceptFunc(dh); got != dns.MsgIgnore {
		t.Fatalf("UPDATE response: got %v, want MsgIgnore", got)
	}
}

func TestMsgAcceptStillAllowsQuery(t *testing.T) {
	var dh dns.Header
	dh.Qdcount = 1
	if got := dns.DefaultMsgAcceptFunc(dh); got != dns.MsgAccept {
		t.Fatalf("QUERY: got %v, want MsgAccept", got)
	}
}
