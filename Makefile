default: lint test

build:
	go build \
		-trimpath \
		-mod=readonly \
		-ldflags "-s -w -X main.version=$(shell git describe --tags --always || echo dev)" \
		-o ship-oci

lint:
	golangci-lint run ./...

test:
	go test -v -cover ./...

trivy:
	trivy fs . \
		--dependency-tree \
		--exit-code 1 \
		--format table \
		--ignore-unfixed \
		--quiet \
		--scanners misconfig,license,secret,vuln \
		--severity HIGH,CRITICAL
