package main

import (
	"github.com/vmihailenco/msgpack"
)

// logEntry is the same as dnsLog without some fields which are not required
// for fluentd outputs.
type logEntry struct {
	QueryID             uint16 `msgpack:"query_id"`
	ResponseCode        int    `msgpack:"rcode"`
	Question            string `msgpack:"q"`
	QuestionType        string `msgpack:"qtype"`
	Answer              string `msgpack:"a"`
	AnswerType          string `msgpack:"atype"`
	TTL                 uint32 `msgpack:"ttl"`
	Server              string `msgpack:"dst"`
	Client              string `msgpack:"src"`
	Timestamp           string `msgpack:"tstamp"`
	Elapsed             int64  `msgpack:"elapsed"`
	ClientPort          uint16 `msgpack:"sport"`
	Level               string `msgpack:"level,omitempty"` // syslog level omitted if empty
	Length              int    `msgpack:"bytes"`
	Proto               string `msgpack:"protocol"`
	Truncated           bool   `msgpack:"truncated"`
	AuthoritativeAnswer bool   `msgpack:"aa"`
	RecursionDesired    bool   `msgpack:"rd"`
	RecursionAvailable  bool   `msgpack:"ra"`
}

// MarshalMsgpack returns the binary messagepack encoded log entry.
func (dle *DNSLogEntry) MarshalMsgpack() ([]byte, error) {
	return msgpack.Marshal(&logEntry{
		QueryID:             dle.QueryID,
		ResponseCode:        dle.ResponseCode,
		Question:            dle.Question,
		QuestionType:        dle.QuestionType,
		Answer:              dle.Answer,
		AnswerType:          dle.AnswerType,
		TTL:                 dle.TTL,
		Server:              dle.Server.String(),
		Client:              dle.Client.String(),
		Timestamp:           dle.Timestamp,
		Elapsed:             dle.Elapsed,
		ClientPort:          dle.ClientPort,
		Level:               dle.Level,
		Length:              dle.Length,
		Proto:               dle.Proto,
		Truncated:           dle.Truncated,
		AuthoritativeAnswer: dle.AuthoritativeAnswer,
		RecursionDesired:    dle.RecursionDesired,
		RecursionAvailable:  dle.RecursionAvailable,
	})
}

// UnmarshalMsgpack returns the unmarshaled entry (not currently comnplete.)
func (dle *DNSLogEntry) UnmarshalMsgpack(data []byte) error {
	tmp := &DNSLogEntry{}
	if err := msgpack.Unmarshal(data, &tmp); err != nil {
		return err
	}

	return nil
}
