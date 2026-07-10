package splunk

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"errors"
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
	AppContext                  string
	SessionKey                  SessionKey
	AuthToken                   string
	TLSVerify                   bool
	Timeout                     time.Duration
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

const (
	dispatchStateDone      = "DONE"
	dispatchStateFailed    = "FAILED"
	dispatchStateCancelled = "CANCELLED"
	pollInterval           = time.Second
)

// Web Methods

func (conn SplunkConnection) httpClient() *http.Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: !conn.TLSVerify},
	}
	client := &http.Client{Transport: tr, Timeout: conn.Timeout}
	return client
}

func (conn SplunkConnection) httpGet(ctx context.Context, url string, data *url.Values) (string, error) {
	return conn.httpCall(ctx, url, http.MethodGet, data)
}

func (conn SplunkConnection) httpPost(ctx context.Context, url string, data *url.Values) (string, error) {
	return conn.httpCall(ctx, url, http.MethodPost, data)
}

func (conn SplunkConnection) httpCall(ctx context.Context, requestURL string, method string, data *url.Values) (string, error) {
	client := conn.httpClient()

	var payload io.Reader
	if method == http.MethodGet && data != nil {
		parsedURL, err := url.Parse(requestURL)
		if err != nil {
			return "", err
		}
		parsedQuery := parsedURL.Query()
		for key, values := range *data {
			for _, value := range values {
				parsedQuery.Add(key, value)
			}
		}
		parsedURL.RawQuery = parsedQuery.Encode()
		requestURL = parsedURL.String()
	} else if data != nil {
		payload = bytes.NewBufferString(data.Encode())
	}

	request, err := http.NewRequestWithContext(ctx, method, requestURL, payload)
	if err != nil {
		return "", err
	}
	if method == http.MethodPost && data != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	conn.addAuthHeader(request)
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	body, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		return "", readErr
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("request failed with status %s for %s: %s", response.Status, safeURLForLog(requestURL), responseBodyForError(body))
	}
	return string(body), nil
}

func responseBodyForError(body []byte) string {
	const maxErrorBodyBytes = 2048

	trimmedBody := strings.TrimSpace(string(body))
	if len(trimmedBody) <= maxErrorBodyBytes {
		return trimmedBody
	}
	return fmt.Sprintf("%s... [truncated %d bytes]", trimmedBody[:maxErrorBodyBytes], len(trimmedBody)-maxErrorBodyBytes)
}

func safeURLForLog(rawURL string) string {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "url=<invalid>"
	}

	path := parsedURL.EscapedPath()
	if path == "" {
		path = "/"
	}

	return fmt.Sprintf("scheme=%s host=%s path=%s", parsedURL.Scheme, parsedURL.Host, path)
}

func (conn SplunkConnection) addAuthHeader(request *http.Request) {
	// use auth token first if provided. then session key if already obtained. login with credentials last
	if conn.AuthToken != "" {
		request.Header.Add("Authorization", fmt.Sprintf("Bearer %s", conn.AuthToken))
	} else if conn.SessionKey.Value != "" {
		request.Header.Add("Authorization", fmt.Sprintf("Splunk %s", conn.SessionKey.Value))
	} else {
		request.SetBasicAuth(conn.Username, conn.Password)
	}
}

// Splunk Methods

// Login connects to the Splunk server and retrieves a session key.
func (conn *SplunkConnection) Login(ctx context.Context) error {
	// exit early if auth token is already configured.
	if conn.AuthToken != "" {
		return nil
	}
	if conn.Username == "" || conn.Password == "" {
		return errors.New("SPLUNKUSERNAME and SPLUNKPASSWORD are required when SPLUNKTOKEN is not set")
	}

	data := make(url.Values)
	data.Add("username", conn.Username)
	data.Add("password", conn.Password)
	data.Add("output_mode", "json")
	response, err := conn.httpPost(ctx, fmt.Sprintf("%s/services/auth/login", conn.BaseURL), &data)
	if err != nil {
		return err
	}
	if strings.Contains(response, "Login failed") || strings.Contains(response, "Unauthorized") {
		return fmt.Errorf("%s", response)
	}

	var key SessionKey
	if err := json.Unmarshal([]byte(response), &key); err != nil {
		return err
	}
	if key.Value == "" {
		return errors.New("could not parse sessionKey from login response")
	}
	conn.SessionKey = key
	return nil
}

// Return URL string formatted with job sid.
func (conn SplunkConnection) jobURL(query *SplunkQuery) string {
	return fmt.Sprintf("%s/services/search/jobs/%s", conn.BaseURL, query.Job.Sid)
}

// Check on job status until terminal state or context deadline.
func (conn SplunkConnection) jobStatus(ctx context.Context, query *SplunkQuery) error {
	data := make(url.Values)
	data = conn.namespaceValues(data)
	query.State = "DISPATCHED"
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			query.State = "CANCELLED"
			return ctx.Err()
		case <-ticker.C:
		}

		response, err := conn.httpGet(ctx, conn.jobURL(query), &data)
		if err != nil {
			query.State = "ERROR"
			return err
		}

		var jobStatus SplunkJobStatus
		if err := xml.Unmarshal([]byte(response), &jobStatus); err != nil {
			query.State = "ERROR"
			return err
		}

		for _, v := range jobStatus.Content.Dict.Key {
			if v.Name != "dispatchState" {
				continue
			}
			query.State = v.Text
			switch query.State {
			case dispatchStateDone:
				return nil
			case dispatchStateFailed, dispatchStateCancelled:
				return fmt.Errorf("splunk job ended in %s state", query.State)
			}
		}
	}
}

// Write results bytes to file as unmodified JSON.
// Something else like python etc can be used on the saved API response.
func (conn SplunkConnection) writeResults(query *SplunkQuery, outputfile string) error {
	return os.WriteFile(outputfile, query.Results, 0644)
}

// Fetch job results.
func (conn SplunkConnection) jobResults(ctx context.Context, query *SplunkQuery) error {
	data := make(url.Values)
	data = conn.namespaceValues(data)
	data.Add("output_mode", "json")

	url := fmt.Sprintf("%s/results/", conn.jobURL(query))
	response, err := conn.httpGet(ctx, url, &data)
	if err != nil {
		query.Results = nil
		return err
	}

	query.Results = []byte(response)
	return nil
}

// Dispatch Splunk Query: Main Entry Method.
func (conn SplunkConnection) DispatchQuery(ctx context.Context, query *SplunkQuery, outputfile string) error {
	data := make(url.Values)
	data = conn.namespaceValues(data)
	data.Add("search", query.Query)

	response, err := conn.httpPost(ctx, fmt.Sprintf("%s/services/search/jobs/", conn.BaseURL), &data)
	if err != nil {
		return err
	}
	if strings.Contains(response, "Unauthorized") {
		return fmt.Errorf("%s", response)
	}

	if err = xml.Unmarshal([]byte(response), &query.Job); err != nil {
		return err
	}
	if query.Job.Sid == "" {
		return fmt.Errorf("empty job sid in response")
	}

	if err = conn.jobStatus(ctx, query); err != nil {
		return err
	}
	if query.State != dispatchStateDone {
		return fmt.Errorf("unexpected job terminal state: %s", query.State)
	}

	if err = conn.jobResults(ctx, query); err != nil {
		return err
	}
	return conn.writeResults(query, outputfile)
}

func (conn SplunkConnection) namespaceValues(values url.Values) url.Values {
	if values == nil {
		values = make(url.Values)
	}
	trimmedAppContext := strings.TrimSpace(conn.AppContext)
	if trimmedAppContext != "" {
		values.Add("namespace", trimmedAppContext)
	}
	return values
}
