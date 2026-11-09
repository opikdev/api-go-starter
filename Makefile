.PHONY: generate lint test coverage check-generated check-migrations verify e2e run migrate seed

generate:
	go generate ./...

lint:
	test -z "$$(gofmt -l .)"
	go vet ./...
	golangci-lint run ./...

test:
	go test -race ./...

coverage:
	go test -race -coverpkg=./internal/... -coverprofile=coverage.out ./...
	grep -vE 'api\.gen\.go|client\.gen\.go|/store/(db|models|.*\.sql)\.go' coverage.out > coverage-hand.out
	scripts/check-coverage.sh coverage-hand.out 80

check-generated: generate
	git diff --exit-code -- internal/api internal/apiclient internal/store

check-migrations:
	scripts/check-migrations.sh

verify: lint check-generated check-migrations coverage
	.agents/check.sh
	.github/scripts/check-repo-standards.sh

e2e:
	go test -count=1 -tags e2e ./test/e2e/...

run:
	scripts/with-env.sh go run ./cmd/api serve

migrate:
	scripts/with-env.sh go run ./cmd/api migrate

seed:
	scripts/with-env.sh go run ./cmd/api seed
