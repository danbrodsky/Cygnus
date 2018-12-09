package tests

import (
	"../host"
	"testing"
)

/*
This test no longer passes because we are choosing hosts based on latency.
*/
func TestFlooding(t *testing.T) {
	clientIp := "13.77.176.89"

	host.Initialize("../parameters/parameters1.json") //Vancouver
	host.Initialize("../parameters/parameters2.json") //Berlin
	host3 := host.Initialize("../parameters/parameters3.json") //London
	host.Initialize("../parameters/parameters4.json") //Beijing
	host5 := host.Initialize("../parameters/parameters5.json") //Osaka
	host.Initialize("../parameters/parameters6.json") //Seattle

	expectedHost := "127.0.0.1:5000"
	foundHost := host5.FindHostForClient("jcho", clientIp + ":5050")
	if foundHost != expectedHost {
		t.Errorf("Expected %s, got %s", expectedHost, foundHost)
	}

	expectedHost = "127.0.0.1:9000"
	foundHost = host3.FindHostForClient("jcho", clientIp + ":5052")
	if foundHost != expectedHost {
		t.Errorf("Expected %s, got %s", expectedHost, foundHost)
	}

	expectedHost = "127.0.0.1:4000"
	foundHost = host5.FindHostForClient("jcho", clientIp + ":5051")
	if foundHost != expectedHost {
		t.Errorf("Expected %s, got %s", expectedHost, foundHost)
	}
}
