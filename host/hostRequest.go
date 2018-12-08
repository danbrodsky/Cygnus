package host

import (
	"bytes"
	"encoding/gob"
)

type HostRequestWithSender struct {
	Request 	HostRequest
	Sender 		string
}

type HostRequest struct {
	SequenceNumber		uint64
	ClientLocation		Location
	BestHostLocation	Location
	RequestingHost 		string
	BestHost			string
	RespondingHost		string
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

type Location struct {
	Latitude  float64
	Longitude float64
}

type Parameters struct {
	HostLatitude      float64  `json:"HostLatitude"`
	HostLongitude     float64  `json:"HostLongitude"`
	HostID            string   `json:"HostID"`
	PeerHosts         []string `json:"PeerHosts"`
	HostPublicIP      string   `json:"HostPublicIP"`
	HostPrivateIP     string   `json:"HostPrivateIP"`
	AcceptClientsPort string   `json:"AcceptClientsPort"`
	HostsPortRPC      string   `json:"HostsPortRPC"`
	HostsPortUDP      string   `json:"HostsPortUDP"`
}

func marshallHostRequest(hb HostRequest) ([]byte, error) {
	var network bytes.Buffer
	enc := gob.NewEncoder(&network)
	err := enc.Encode(hb)
	return network.Bytes(), err
}

func unMarshallHostRequest(buffer []byte, n int) (HostRequest, error) {
	var hr HostRequest
	bufDecoder := bytes.NewBuffer(buffer[0:n])
	decoder := gob.NewDecoder(bufDecoder)
	decoderErr := decoder.Decode(&hr)
	return hr, decoderErr
}
