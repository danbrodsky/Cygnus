package tests

import (
	"../host"
	"testing"
)

func TestFlooding(t *testing.T) {
	host.Initialize("../parameters/parameters1.json") //Vancouver
	host.Initialize("../parameters/parameters2.json") //Berlin
	host.Initialize("../parameters/parameters3.json") //London
	host.Initialize("../parameters/parameters4.json") //Beijing
	host5 := host.Initialize("../parameters/parameters5.json") //Osaka

	//Client is in Richmond, BC. Should be closes to the host in Vancouver
	clientLocation := host.Location{Latitude: 49.166592, Longitude: -123.133568}
	expectedHost := "127.0.0.1:5000"
	var foundHost string
	host5.FindHostForClient(clientLocation, &foundHost)
	if foundHost != expectedHost {
		t.Errorf("Expected %s, got %s", expectedHost, foundHost)
	}
	/*
	host5.FindHostForClient(clientLocation, &foundHost)
	if foundHost != expectedHost {
		t.Errorf("Expected %s, got %s", expectedHost, foundHost)
	}

	//Client is in Tokyo. Should be closes to the host in Osaka
	clientLocation2 := host.Location{Latitude: 35.689487, Longitude: 139.691711}
	expectedHost2 := "127.0.0.1:9000"
	host4.FindHostForClient(clientLocation2, &foundHost)
	if foundHost != expectedHost2 {
		t.Errorf("Expected %s, got %s", expectedHost2, foundHost)
	}

	host4.FindHostForClient(clientLocation2, &foundHost)
        if foundHost != expectedHost2 {
                t.Errorf("Expected %s, got %s", expectedHost2, foundHost)
        }
	*/


}
