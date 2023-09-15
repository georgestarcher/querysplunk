# splunkquery

## Dependancies
If you build from source you will need package(s)
* https://github.com/joho/godotenv

## .env File:
You may use a .env file if you use the `-e true` option. Otherwise it will default from attempting to load the following from OS Environment Variables.

```
SPLUNKUSERNAME=
SPLUNKPASSWORD=
SPLUNKBASEURL=
SPLUNKTOKEN=
SPLUNKTIMEOUT=120
SPLUNKTLSVERIFY=true
```

* You can use credentials or a Splunk Authentication token. If you use SPLUNKTOKEN it will ignore the credentials or lack of them.
* You can set SPLUNKTLSVERIFY to false to avoid validating a Splunk TLS Certificate. If the value fails to convert to boolean type properly it will default to false.
* SPLUNKTIMEOUT will default to 120 seconds if not specified. This is the max time the program will keep checking for the dispatched query to reach a DONE state.

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
``

Usage of ./splunkquery-darwin:
  -e string
        Use .env file (default "false")
  -o string
        Enter the filename to save results. (default "splunkresults.json")
  -q string
        Enter the filename of the Query. (default "query.txt")
