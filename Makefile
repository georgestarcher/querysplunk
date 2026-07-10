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
	rm -rf build ${BUILD_DIR}
	mkdir -p build ${BUILD_DIR}
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o build/${BINARY_NAME}-${VERSION}-darwin-amd64 .
	tar -C build -czf ${BUILD_DIR}/${BINARY_NAME}-${VERSION}-darwin-amd64.tar.gz ${BINARY_NAME}-${VERSION}-darwin-amd64
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o build/${BINARY_NAME}-${VERSION}-darwin-arm64 .
	tar -C build -czf ${BUILD_DIR}/${BINARY_NAME}-${VERSION}-darwin-arm64.tar.gz ${BINARY_NAME}-${VERSION}-darwin-arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o build/${BINARY_NAME}-${VERSION}-linux-amd64 .
	tar -C build -czf ${BUILD_DIR}/${BINARY_NAME}-${VERSION}-linux-amd64.tar.gz ${BINARY_NAME}-${VERSION}-linux-amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o build/${BINARY_NAME}-${VERSION}-linux-arm64 .
	tar -C build -czf ${BUILD_DIR}/${BINARY_NAME}-${VERSION}-linux-arm64.tar.gz ${BINARY_NAME}-${VERSION}-linux-arm64
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o build/${BINARY_NAME}-${VERSION}-windows-amd64.exe .
	zip -j ${BUILD_DIR}/${BINARY_NAME}-${VERSION}-windows-amd64.zip build/${BINARY_NAME}-${VERSION}-windows-amd64.exe
	(cd ${BUILD_DIR} && shasum -a 256 * > checksums.txt)

clean:
	go clean
	rm -rf build ${BUILD_DIR}
