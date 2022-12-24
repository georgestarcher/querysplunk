package splunk

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Data Structures

type SplunkConnection struct {
	Username, Password, BaseURL string
	Sessionkey                  SessionKey
	Authtoken                   string
	TLSverify                   bool
	Timeout                     int
}

type SessionKey struct {
	Value string `json:"sessionKey"`
}

type SplunkJob struct {
	XMLName xml.Name `xml:"response"`
	Text    string   `xml:",chardata"`
	Sid     string   `xml:"sid"`
}

type SplunkQuery struct {
	Query   string
	Job     SplunkJob
	State   string
	Results []byte
}

type SplunkJobStatus struct {
	XMLName    xml.Name `xml:"entry"`
	Text       string   `xml:",chardata"`
	Xmlns      string   `xml:"xmlns,attr"`
	S          string   `xml:"s,attr"`
	Opensearch string   `xml:"opensearch,attr"`
	Title      string   `xml:"title"`
	ID         string   `xml:"id"`
	Updated    string   `xml:"updated"`
	Link       []struct {
		Text string `xml:",chardata"`
		Href string `xml:"href,attr"`
		Rel  string `xml:"rel,attr"`
	} `xml:"link"`
	Published string `xml:"published"`
	Author    struct {
		Text string `xml:",chardata"`
		Name string `xml:"name"`
	} `xml:"author"`
	Content struct {
		Text string `xml:",chardata"`
		Type string `xml:"type,attr"`
		Dict struct {
			Key []struct {
				Text string `xml:",chardata"`
				Name string `xml:"name,attr"`
				Dict struct {
					Text string `xml:",chardata"`
					Key  []struct {
						Text string `xml:",chardata"`
						Name string `xml:"name,attr"`
						Dict struct {
							Text string `xml:",chardata"`
							Key  []struct {
								Text string `xml:",chardata"`
								Name string `xml:"name,attr"`
								List struct {
									Text string `xml:",chardata"`
									Item string `xml:"item"`
								} `xml:"list"`
							} `xml:"key"`
						} `xml:"dict"`
					} `xml:"key"`
				} `xml:"dict"`
				List string `xml:"list"`
			} `xml:"key"`
		} `xml:"dict"`
	} `xml:"content"`
}

// Web Methods

func httpClient() *http.Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr}
	return client
}

func (conn SplunkConnection) httpGet(url string, data *url.Values) (string, error) {
	return conn.httpCall(url, "GET", data)
}

func (conn SplunkConnection) httpPost(url string, data *url.Values) (string, error) {
	return conn.httpCall(url, "POST", data)
}

func (conn SplunkConnection) httpCall(url string, method string, data *url.Values) (string, error) {
	client := httpClient()

	var payload io.Reader
	if data != nil {
		payload = bytes.NewBufferString(data.Encode())
	}

	request, err := http.NewRequest(method, url, payload)

	if err != nil {
		return "", err
	}

	conn.addAuthHeader(request)
	response, err := client.Do(request)

	if err != nil {
		return "", err
	}

	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	return string(body), nil
}

func (conn SplunkConnection) addAuthHeader(request *http.Request) {

	// use auth token first if provided. then session key if alreay obtained. login with credentials last resort
	if conn.Authtoken != "" {
		request.Header.Add("Authorization", fmt.Sprintf("Bearer %s", conn.Authtoken))
	} else if conn.Sessionkey.Value != "" {
		request.Header.Add("Authorization", fmt.Sprintf("Splunk %s", conn.Sessionkey.Value))
	} else {
		request.SetBasicAuth(conn.Username, conn.Password)
	}
}

// Splunk Methods

// Login connects to the Splunk server and retrieves a session key
func (conn *SplunkConnection) Login() error {

	// exit function if auth token being used no further login action is needed
	if conn.Authtoken != "" {
		return nil
	}
	data := make(url.Values)
	data.Add("username", conn.Username)
	data.Add("password", conn.Password)
	data.Add("output_mode", "json")
	response, err := conn.httpPost(fmt.Sprintf("%s/services/auth/login", conn.BaseURL), &data)

	if err != nil {
		return err
	}
	if strings.Contains(response, "Login failed") {
		return fmt.Errorf("%s", response)
	}
	if strings.Contains(response, "Unauthorized") {
		return fmt.Errorf("%s", response)
	}

	bytes := []byte(response)
	var key SessionKey
	unmarshall_error := json.Unmarshal(bytes, &key)
	conn.Sessionkey = key

	return unmarshall_error
}

// Return URL string formatted with job sid
func (conn SplunkConnection) jobUrl(query *SplunkQuery) string {

	url := fmt.Sprintf("%s/services/search/jobs/%s", conn.BaseURL, query.Job.Sid)

	return url
}

// Check on job status until DONE or timeout reached
func (conn SplunkConnection) jobStatus(query *SplunkQuery) error {

	data := make(url.Values)
	query.State = "DISPATCHED"
	var i int

	for i < conn.Timeout {

		time.Sleep(time.Second * 1)
		response, err := conn.httpGet(conn.jobUrl(query), &data)

		if err != nil {
			query.State = "ERROR"
			return err
		}

		bytes := []byte(response)
		var jobStatus SplunkJobStatus

		unmarshall_error := xml.Unmarshal(bytes, &jobStatus)

		if unmarshall_error != nil {
			query.State = "ERROR"
			return err
		}

		for _, v := range jobStatus.Content.Dict.Key {
			if v.Name == "dispatchState" {
				query.State = v.Text
				if v.Text == "DONE" {
					return err
				}
			}
		}

		i += 1
	}
	query.State = "TIMEOUT"
	return fmt.Errorf("query exceeds 120s timeout")

}

// Write results bytes to file as unmodified JSON
// Something else like python etc can be used on the saved API response
func (conn SplunkConnection) writeResults(query *SplunkQuery, outputfile string) error {

	f, err := os.Create(outputfile)
	if err != nil {
		return err
	}
	defer f.Close()
	os.WriteFile(outputfile, query.Results, 0644)

	return err

}

// Fetch job results
func (conn SplunkConnection) jobResults(query *SplunkQuery) error {

	data := make(url.Values)
	data.Add("output_mode", "json")

	url := fmt.Sprintf("%s/results/", conn.jobUrl(query))
	response, err := conn.httpGet(url, &data)

	if err != nil {
		query.Results = nil
		return err
	}

	query.Results = []byte(response)

	return err

}

// Dispatch Splunk Query: Main Entry Method
func (conn SplunkConnection) DispatchQuery(query *SplunkQuery, outputfile string) error {

	data := make(url.Values)
	data.Add("search", query.Query)

	response, err := conn.httpPost(fmt.Sprintf("%s/services/search/jobs/", conn.BaseURL), &data)

	if err != nil {
		return err
	}
	if strings.Contains(response, "Unauthorized") {
		return fmt.Errorf("%s", response)
	}

	bytes := []byte(response)
	unmarshall_error := xml.Unmarshal(bytes, &query.Job)

	if unmarshall_error != nil {
		return err
	}

	err = conn.jobStatus(query)
	if err != nil {
		return err
	}

	if query.State == "DONE" {
		conn.jobResults(query)
		if query.Results != nil {
			conn.writeResults(query, outputfile)
		}
	}

	return err
}
