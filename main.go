package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	// import for the .env file support
	"github.com/georgestarcher/querysplunk/splunk"
	"github.com/joho/godotenv"
)

// setup more standard logging format
type logWriter struct {
}

func (writer logWriter) Write(bytes []byte) (int, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return 0, err
	}

	message := []byte(time.Now().UTC().Format("2006-01-02T15:04:05.999Z") + " " + hostname + " splunkquery [DEBUG] " + string(bytes))
	written, err := os.Stdout.Write(message)
	if err != nil {
		return written, err
	}

	// The logger contract expects the original payload length to be reported as written.
	return len(bytes), nil
}

func timeoutFromEnv() (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv("SPLUNKTIMEOUT"))
	if value == "" {
		return 120 * time.Second, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("invalid SPLUNKTIMEOUT value %q; must be a positive integer (seconds)", value)
	}
	return time.Duration(parsed) * time.Second, nil
}

func tlsVerifyFromEnv() (bool, error) {
	value := strings.TrimSpace(os.Getenv("SPLUNKTLSVERIFY"))
	if value == "" {
		return true, nil
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return true, fmt.Errorf("invalid SPLUNKTLSVERIFY value %q; must be true or false", value)
	}
	return parsed, nil
}

func main() {
	var queryFile string
	var outputFile string
	var useEnvFile bool
	var appContext string

	log.SetFlags(0)
	log.SetOutput(new(logWriter))

	flag.BoolVar(&useEnvFile, "e", false, "Use .env file")
	flag.StringVar(&appContext, "app", "", "Splunk app context (namespace) for query execution")
	flag.StringVar(&queryFile, "q", "query.txt", "Enter the filename of the Query")
	flag.StringVar(&outputFile, "o", "splunkresults.json", "Enter the filename to save results")
	flag.Parse()

	if useEnvFile {
		if err := godotenv.Load(); err != nil {
			log.Fatal("ERROR: could not load .env file")
		}
	}

	if appContext == "" {
		appContext = os.Getenv("SPLUNKAPP")
	}
	appContext = strings.TrimSpace(appContext)

	fileContent, err := os.ReadFile(queryFile)
	if err != nil {
		log.Fatal(err)
	}
	splunkQueryString := string(fileContent)

	username := os.Getenv("SPLUNKUSERNAME")
	password := os.Getenv("SPLUNKPASSWORD")
	baseURL := strings.TrimRight(os.Getenv("SPLUNKBASEURL"), "/")
	splunkToken := os.Getenv("SPLUNKTOKEN")

	if splunkToken == "" && (username == "" || password == "") {
		log.Fatal("ERROR: missing SPLUNKUSERNAME and/or SPLUNKPASSWORD when SPLUNKTOKEN is not set")
	}
	if baseURL == "" {
		log.Fatal("ERROR: Missing SPLUNKBASEURL")
	}

	tlsVerify, err := tlsVerifyFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	timeout, err := timeoutFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn := splunk.SplunkConnection{
		AppContext: appContext,
		Username:   username,
		Password:   password,
		AuthToken:  splunkToken,
		BaseURL:    baseURL,
		TLSVerify:  tlsVerify,
		Timeout:    timeout,
	}

	if err = conn.Login(ctx); err != nil {
		log.Fatalf("ERROR: Couldn't login to Splunk: %s", err)
	}

	splunkQuery := splunk.SplunkQuery{Query: splunkQueryString}
	if err = conn.DispatchQuery(ctx, &splunkQuery, outputFile); err != nil {
		log.Fatalf("ERROR: %s", err)
	}

	log.Print("SUCCESS: Query Completed")
}
