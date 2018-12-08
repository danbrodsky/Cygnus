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
	SenderVerificationLAddr string
}

type HostClientPair struct {
	SequenceNumber		uint64
	Client				string
	Host				string
}

type Location struct {
	Latitude  float64
	Longitude float64
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
