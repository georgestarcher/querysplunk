module github.com/georgestarcher/querysplunk

go 1.26

require (
	github.com/georgestarcher/querysplunk/splunk v0.0.0
	github.com/joho/godotenv v1.4.0
)

require gopkg.in/yaml.v3 v3.0.1

replace github.com/georgestarcher/querysplunk/splunk => ./splunk
