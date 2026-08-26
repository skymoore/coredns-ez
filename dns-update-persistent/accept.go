package dnsupdatepersist

import (
	"github.com/miekg/dns"
)

// miekg/dns.DefaultMsgAcceptFunc rejects every opcode other than QUERY and
// NOTIFY with NOTIMP, before CoreDNS's plugin chain runs. RFC 2136 UPDATE
// never reaches this plugin unless that default is replaced. init wraps the
// previous function so QUERY/NOTIFY keep the original limits.
func init() { allowRFC2136Updates() }

func allowRFC2136Updates() {
	prev := dns.DefaultMsgAcceptFunc
	dns.DefaultMsgAcceptFunc = func(dh dns.Header) dns.MsgAcceptAction {
		opcode := int(dh.Bits>>11) & 0xF
		if opcode == dns.OpcodeUpdate {
			if dh.Bits&0x8000 != 0 { // QR: a response, not a request
				return dns.MsgIgnore
			}
			// RFC 2136: exactly one Zone (question). Prerequisites live in
			// Answer, the update list in Authority, TSIG in Additional — all
			// three can be large, which is why the default function rejects
			// UPDATE in the first place.
			if dh.Qdcount != 1 {
				return dns.MsgReject
			}
			return dns.MsgAccept
		}
		return prev(dh)
	}
}
