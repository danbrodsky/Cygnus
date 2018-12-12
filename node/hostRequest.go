package node

import (
	"bytes"
	"encoding/gob"
	"time"
)

type HostRequestWithSender struct {
	Request 	HostRequest
	Sender 		string
}

type HostRequest struct {
	SequenceNumber		uint64
	ClientAddr			string
	RequestingHost 		string
}

type HostResponse struct {
	SequenceNumber		uint64
	RequestingHost 		string
	RespondingHostAddr	string
	RespondingHost		string
	AvgRTT				time.Duration
	SenderVerificationLAddr string
}

type HostClientPair struct {
	SequenceNumber		uint64
	Client				string
	Host				string
	SendingHost 		string
}

type PairAck struct {
	SequenceNumber 	uint64
	TargetHost 		string
	Accept			bool
}

type HeartBeat struct {
	SeqNum uint64
	Sender string
}
type Ack struct {
	HBeatSeqNum uint64
	Sender      string
}

type VerificationMesssage struct {
        HostId string
        ReturnIp string
}

type DecisionMessage struct{
        HostId string
        Decision bool
}

type LedgerEntry struct {
	HostId string
	ClientId string
	StartTime time.Time
	EndTime time.Time
}

type Parameters struct {
		NodeID            			string   	`json:"NodeID"`
        PeerHosts         			[]string 	`json:"PeerHosts"`
        HostPublicIP      			string   	`json:"HostPublicIP"`
        HostPrivateIP     			string   	`json:"HostPrivateIP"`
        AcceptClientsPort 			string   	`json:"AcceptClientsPort"`
        HostsPortRPC      			string   	`json:"HostsPortRPC"`
        HostsPortUDP      			string   	`json:"HostsPortUDP"`
        VerificationPortUDP      	string   	`json:"VerificationPortUDP"`
        VerificationReturnPortUDP	string		`json:"VerificationReturnPortUDP"`
        BlackList         			[]string  	`json:"BlackList"`
		Available					bool		`json:"Available"`
}

func marshallHostResponse(hb HostResponse) ([]byte, error) {
	var network bytes.Buffer
	enc := gob.NewEncoder(&network)
	err := enc.Encode(hb)
	return network.Bytes(), err
}

func unMarshallHostResponse(buffer []byte, n int) (HostResponse, error) {
	var hr HostResponse
	bufDecoder := bytes.NewBuffer(buffer[0:n])
	decoder := gob.NewDecoder(bufDecoder)
	decoderErr := decoder.Decode(&hr)
	return hr, decoderErr
}
