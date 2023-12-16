package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"splunk"
	"strconv"
	"sync"
	"time"

	// import for the .env file support
	"github.com/joho/godotenv"
)

// setup more standard logging format
type logWriter struct {
}

func (writer logWriter) Write(bytes []byte) (int, error) {
	hostname, err := os.Hostname()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	return fmt.Print(time.Now().UTC().Format("2006-01-02T15:04:05.999Z") + " " + hostname + " splunkquery [DEBUG] " + string(bytes))
}

func main() {

	var queryfile string
	var outputfile string
	var envfile string

	// get optional flag arguments
	log.SetFlags(0)
	log.SetOutput(new(logWriter))

	flag.StringVar(&envfile, "e", "false", "Use .env file")
	flag.StringVar(&queryfile, "q", "query.txt", "Enter the filename of the Query.")
	flag.StringVar(&outputfile, "o", "splunkresults.json", "Enter the filename to save results.")
	flag.Parse()

	// use .env file if option chosen
	useEnvFile, _ := strconv.ParseBool(envfile)
	if useEnvFile {

		err := godotenv.Load()
		if err != nil {
			log.Fatal("Error loading .env file")
		}

	}

	// read in SPL query to be executed. One query per file
	fileContent, err := os.ReadFile(queryfile)
	if err != nil {
		log.Fatal(err)
	}
	splunkQueryString := string(fileContent)

	// setup goroutine waitgroup for thread completion
	var wg sync.WaitGroup

	// read environment variables
	username := os.Getenv("SPLUNKUSERNAME")
	password := os.Getenv("SPLUNKPASSWORD")
	baseurl := os.Getenv("SPLUNKBASEURL")
	splunktoken := os.Getenv("SPLUNKTOKEN")

	// use credentials if auth token not used
	if username == "" && splunktoken == "" {
		log.Fatalf("ERROR: Missing Username")
	}
	if password == "" && splunktoken == "" {
		log.Fatalf("ERROR: Missing Password")
	}
	if baseurl == "" {
		log.Fatalf("ERROR: Missing BaseURL")
	}

	tlsVerify, err := strconv.ParseBool(os.Getenv("SPLUNKTLSVERIFY"))
	if err != nil {
		tlsVerify = false
	}

	// set max timeout to wait for a query to finish. default to 120 seconds
	timeout, err := strconv.Atoi(os.Getenv("SPLUNKTIMEOUT"))
	if err != nil {
		timeout = 120
	}

	// setup splunk connection structure
	conn := splunk.SplunkConnection{
		Username:  username,
		Password:  password,
		Authtoken: splunktoken,
		BaseURL:   baseurl,
		TLSverify: tlsVerify,
		Timeout:   timeout,
	}

	// attempt to login using credentials and obtain a session key
	err = conn.Login()
	if err != nil {
		log.Fatalf("ERROR: Couldn't login to splunk: %s", err)
	}

	//setup the query structure and dispatch the job
	splunkQuery := splunk.SplunkQuery{Query: splunkQueryString}

	wg.Add(1)
	go func() {
		defer wg.Done()
		err = conn.DispatchQuery(&splunkQuery, outputfile)
	}()

	wg.Wait()

	if err != nil {
		log.Fatalf("ERROR: %s", err)
	} else {
		log.Print("SUCCESS: Query Completed")
	}

}
