BINARY_NAME=splunkquery
SOURCE_NAME=main.go
BUILD_DIR=dist

build:
	GOARCH=amd64 GOOS=darwin go build -o ${BUILD_DIR}/${BINARY_NAME}-darwin ${SOURCE_NAME}
	GOARCH=amd64 GOOS=linux go build -o ${BUILD_DIR}/${BINARY_NAME}-linux ${SOURCE_NAME}
	GOARCH=amd64 GOOS=windows go build -o ${BUILD_DIR}/${BINARY_NAME}-windows.exe ${SOURCE_NAME}

run:
	${BUILD_DIR}/${BINARY_NAME}-darwin

build_and_run: build run

clean:
	go clean
	rm ${BUILD_DIR}/${BINARY_NAME}-darwin
	rm ${BUILD_DIR}/${BINARY_NAME}-linux
	rm ${BUILD_DIR}/${BINARY_NAME}-windows.exe
