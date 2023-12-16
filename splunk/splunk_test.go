package splunk

import (
	"testing"
)

// Test Setup and Calling Splunk Login. Expected Error
func TestSplunkConnection(t *testing.T) {

	// setup splunk connection structure
	conn := SplunkConnection{
		Username:  "admin",
		Password:  "changeme",
		Authtoken: "",
		BaseURL:   "https://localhost:8089/",
		TLSverify: false,
		Timeout:   120,
	}

	// attempt to login to get known error and compare
	err := conn.Login()

	want := "Post \"https://localhost:8089//services/auth/login\": dial tcp [::1]:8089: connect: connection refused"
	got := err.Error()

	if got != want {
		t.Errorf("got %v, wanted %v", got, want)
	}
}
