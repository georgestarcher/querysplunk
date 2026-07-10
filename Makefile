BINARY_NAME=splunkquery
BUILD_DIR=dist

build:
	mkdir -p ${BUILD_DIR}
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -o ${BUILD_DIR}/${BINARY_NAME}-darwin-amd64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -o ${BUILD_DIR}/${BINARY_NAME}-darwin-arm64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o ${BUILD_DIR}/${BINARY_NAME}-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o ${BUILD_DIR}/${BINARY_NAME}-linux-arm64 .
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -o ${BUILD_DIR}/${BINARY_NAME}-windows-amd64.exe .

run:
	${BUILD_DIR}/${BINARY_NAME}-darwin-arm64

build_and_run: build run

clean:
	go clean
	rm -rf ${BUILD_DIR}
