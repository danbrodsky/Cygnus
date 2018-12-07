package tests

import (
	"../host"
	"testing"
	"time"
)

/*
NOTE. When running this test you'll see errors about "reply type not a pointer"
This is because the "FindHostForClient" method doesn't fit the RPC guidelines.
In the future though, "FindHostForClient" will probably only be used internally and not exposed.
It's only like this now so we can test it

The test also won't pass 100% of the time because messages are being sent over UDP.
If any of the messages are lost, the test will fail. This is expected behaviour.
*/
func TestFlooding(t *testing.T) {
	host.Initialize("../parameters/parameters1.json") //Vancouver
	host.Initialize("../parameters/parameters2.json") //Berlin
	host.Initialize("../parameters/parameters3.json") //London
	host4 := host.Initialize("../parameters/parameters4.json") //Beijing
	host5 := host.Initialize("../parameters/parameters5.json") //Osaka
	host.Initialize("../parameters/parameters6.json") //Seattle

	//Client is in Richmond, BC. Should be closes to the host in Vancouver
	clientLocation := host.Location{Latitude: 49.166592, Longitude: -123.133568}
	expectedHost := "127.0.0.1:5000"
	foundHost := host5.FindHostForClient("127.0.0.1:5050", clientLocation)
	if foundHost != expectedHost {
		t.Errorf("Expected %s, got %s", expectedHost, foundHost)
	}

	//Allow some time for the best host to be flooded to the network
	time.Sleep(500 * time.Millisecond)

	//Find a host for another client in Richmond.
	//Since the host with Vancouver is already taken, it should find the host in Seattle
	expectedHost = "127.0.0.1:4000"
	foundHost = host5.FindHostForClient("127.0.0.1:5051", clientLocation)
	if foundHost != expectedHost {
		t.Errorf("Expected %s, got %s", expectedHost, foundHost)
	}

	//Client is in Tokyo. Should be closes to the host in Osaka
	clientLocation = host.Location{Latitude: 35.689487, Longitude: 139.691711}
	expectedHost = "127.0.0.1:9000"
	foundHost = host4.FindHostForClient("127.0.0.1:5052", clientLocation)
	if foundHost != expectedHost {
		t.Errorf("Expected %s, got %s", expectedHost, foundHost)
	}
}
