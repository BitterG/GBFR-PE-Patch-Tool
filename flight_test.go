package main

import "testing"

func TestFlightInputVector(t *testing.T) {
	x, y, z := (flightInput{forward: true, up: true}).vector()
	if x <= 0 || y <= 0 || z != 0 {
		t.Fatalf("forward/up vector = %v, %v, %v", x, y, z)
	}
	if x*x+y*y+z*z < 0.999 || x*x+y*y+z*z > 1.001 {
		t.Fatalf("vector is not normalized: %v", x*x+y*y+z*z)
	}
	if x, y, z := (flightInput{forward: true, back: true, left: true, right: true, up: true, down: true}).vector(); x != 0 || y != 0 || z != 0 {
		t.Fatalf("cancelled input vector = %v, %v, %v", x, y, z)
	}
}

func TestValidFlightSpeed(t *testing.T) {
	if !validFlightSpeed(flightMinimumSpeed) || !validFlightSpeed(flightMaximumSpeed) {
		t.Fatal("valid flight speed boundary rejected")
	}
	if validFlightSpeed(0) || validFlightSpeed(flightMaximumSpeed+1) {
		t.Fatal("invalid flight speed accepted")
	}
}
