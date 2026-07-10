# splunkquery

## Requirements
Go 1.26+ is required.

## Dependencies
If you build from source you will need package(s)
* https://github.com/joho/godotenv

## Quick setup

```bash
brew install go
# or upgrade if already installed
brew upgrade go
```

## .env File:
You may use a .env file with the `-e` flag. Otherwise the tool reads the following from OS Environment Variables.

```
SPLUNKUSERNAME=
SPLUNKPASSWORD=
SPLUNKBASEURL=
SPLUNKTOKEN=
SPLUNKTIMEOUT=120
SPLUNKTLSVERIFY=true
SPLUNKAPP=
```

* You can use credentials or a Splunk Authentication token. If you use SPLUNKTOKEN it will ignore the credentials or lack of them.
* You can set SPLUNKTLSVERIFY to false to avoid validating a Splunk TLS Certificate. If not set, TLS verification defaults to true.
* SPLUNKTIMEOUT will default to 120 seconds if not specified. This is the max time the program will keep checking for the dispatched query to reach a DONE state.
* Use `SPLUNKAPP` (or `-app`) to scope the search to a Splunk app namespace.

## query.txt File:

Place one simple SPL query in the file.
It is recommended to make your SPL Query in Splunk as a saved search. Then make your query file contents like the following.

Bonus that this method of calling a savedsearch works great from SOAR products or SplunkES correlation search drill down fields. I recommend putting such Investigation searches into a SplunkES story as a supporting search. This lets you keep SPL complexity in Splunk as well as document the search there.

```
savedsearch "SOAR - Auth Model - Investigation" user=bob
```

## Usage

1. place the .env file with the desired executable binary

### help
```
./splunkquery-darwin -h
```

Usage of ./splunkquery-darwin:
  -e
        Use .env file
  -o string
        Enter the filename to save results. (default "splunkresults.json")
  -app string
        Splunk app context (namespace) for query execution
  -q string
        Enter the filename of the Query. (default "query.txt")

### integration tests

Build-tagged live tests exist for optional Splunk integration verification.

Run:

```bash
cd splunk
go test -tags integration ./...
```

Required environment variables for integration runs:

- `SPLUNKBASEURL`
- either `SPLUNKTOKEN` or both `SPLUNKUSERNAME` and `SPLUNKPASSWORD`
- optional: `SPLUNKTLSVERIFY`, `SPLUNKTIMEOUT`, `SPLUNKAPP`

### GitHub Actions integration workflow

Repository CI runs unit tests and linting on `push` and `pull_request`.
Live Splunk integration tests are gated to manual runs only.

To run integration tests in GitHub Actions:

1. Go to `Actions` → `Go`
2. Click `Run workflow`
3. Enable **`run_integration_tests`**
4. Start the run

Create these secrets in your repository for the integration step:

- `SPLUNKBASEURL`
- `SPLUNKTOKEN`
- `SPLUNKUSERNAME`
- `SPLUNKPASSWORD`
- `SPLUNKTLSVERIFY`
- `SPLUNKTIMEOUT`
- `SPLUNKAPP`
- `SPLUNK_INTEGRATION_QUERY`

`SPLUNK_INTEGRATION_QUERY` is optional; the default query is:

`search index=_internal | head 1`

You can also pass integration values through the normal environment path as
an alternative to repository secrets.
