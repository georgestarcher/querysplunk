BINARY_NAME=splunkquery
BUILD_DIR=dist
VERSION?=dev

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

package:
	scripts/package-release.sh ${VERSION} ${BINARY_NAME} ${BUILD_DIR}

verify-package:
	scripts/verify-package-contents.sh ${BUILD_DIR} ${VERSION}

clean:
	go clean
	rm -rf build ${BUILD_DIR}
