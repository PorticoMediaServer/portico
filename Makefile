VERSION ?= 0.1.0
SERVER_OUTPUT ?= dist/portico-server
# The official reader capability (CBR and the CB7 surface), from the one checked
# list. A local build is not a release build: it carries these tags but not the
# `release` trust marker.
PORTICO_GO_TAGS ?= $(shell python3 scripts/release-build-tags.py --capability)

.PHONY: test test-go test-clients build-web build-server dev-api dev-web

# Go tests use a disposable PostgreSQL database for the packages that need one
# (PORTICO_TEST_DATABASE_URL_FILE); scripts/runner-test.sh sets that up on a runner.
test: test-go test-clients

test-go:
	cd apikit && ../scripts/env.sh go test ./... && ../scripts/env.sh go vet ./...
	cd server && ../scripts/env.sh go test -tags "$(PORTICO_GO_TAGS)" ./... && ../scripts/env.sh ../scripts/vet-go.sh

test-clients:
	npm run test:core
	npm run check:web

build-web:
	npm run build --workspace portico-web

build-server:
	cd server && CGO_ENABLED=0 ../scripts/env.sh go build -tags "$(PORTICO_GO_TAGS)" -trimpath -o "$(abspath $(SERVER_OUTPUT))" ./cmd/server

dev-api:
	cd server && ../scripts/env.sh go run -tags "$(PORTICO_GO_TAGS)" ./cmd/server

dev-web:
	npm run dev:web
