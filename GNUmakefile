default: build

build:
	go build ./...

install:
	go install .

fmt:
	gofmt -s -w .

lint:
	go vet ./...
	test -z "$$(gofmt -l .)"

# Unit tests: the mock API, plus the examples through the real Terraform CLI (terraform in PATH or TF_ACC_TERRAFORM_PATH).
test:
	TF_ACC= go test ./... -count=1

# Acceptance tests against a real Awacino company (AWACINO_KEY_ID, AWACINO_KEY_SECRET, AWACINO_TEST_DEVICE_ID).
testacc:
	TF_ACC=1 go test ./internal/provider/ -run TestAcc -v -count=1 -timeout 60m

docs:
	go generate ./...

snapshot:
	goreleaser release --snapshot --clean --skip=sign,publish

.PHONY: default build install fmt lint test testacc docs snapshot
