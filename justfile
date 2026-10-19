set dotenv-load := false

pg_container := "workly-postgres"
pg_port := "55432"
pg_url := "postgres://workly:workly@localhost:" + pg_port + "/workly?sslmode=disable"

# List recipes
default:
    @just --list

# Run the local dev server (SQLite, no auth) on :7337, with the dashboard
dev: dashboard
    go run ./cmd/workly dev

# Build the binary into bin/workly, with the dashboard embedded
build: dashboard
    go build -ldflags "-X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o bin/workly ./cmd/workly

# Regenerate the Go server and the dashboard's client from api/openapi.yaml
generate: dashboard-deps
    go generate ./...
    cd dashboard && npm run generate

# Run tests (Postgres tests are skipped)
test:
    go test -race ./...

# Test the Python SDK, including against a server built from this checkout
test-python:
    cd sdks/python && uv run pytest -q

# Test the TypeScript SDK, including against a server built from this checkout
test-typescript: sdk-typescript-deps
    cd sdks/typescript && npm test

# Build a release locally (binaries in dist/, workly-cli wheels in dist/pypi/)
release-snapshot:
    go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean
    python3 packaging/pypi/build_wheels.py

# Build the Docker image for this machine's platform as workly:dev
docker: dashboard
    GOOS=linux CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=dev" -o dist/docker/linux/$(go env GOARCH)/workly ./cmd/workly
    docker build -f deploy/docker/Dockerfile -t workly:dev dist/docker

# Run all tests, including Postgres (starts a container)
test-all: postgres-up
    WORKLY_TEST_POSTGRES_URL="{{pg_url}}" go test -race ./...

# End-to-end tests against real `workly serve` replicas and Postgres (minutes of wall-clock time)
test-e2e: postgres-up
    WORKLY_TEST_POSTGRES_URL="{{pg_url}}" go test -tags e2e -v -count=1 -timeout 15m ./e2e/

# Load test a running server, e.g. `just loadtest -rate 1000` (see docs/performance.md)
loadtest *args:
    go run ./tools/loadtest {{args}}

# Format and vet, and type-check and lint the dashboard and the SDKs
lint: dashboard-deps sdk-typescript-deps docs-deps
    gofmt -l -w .
    go vet ./...
    cd dashboard && npm run format >/dev/null && npm run check
    cd sdks/python && uv run ruff format . && uv run ruff check . && uv run pyright
    cd sdks/typescript && npm run format >/dev/null && npm run check
    cd docs-site && npm run check

# Build the dashboard into dashboard/dist, which the binary embeds
dashboard: dashboard-deps
    cd dashboard && npm run build

# Run the dashboard with hot reload on :5173, against `just dev` on :7337
dashboard-dev: dashboard-deps
    cd dashboard && npm run dev

# Run the docs site (docs.workly.run) with hot reload on :4321; it reads docs/
docs: docs-deps
    cd docs-site && npm run dev

# Build the docs site into docs-site/dist
docs-build: docs-deps
    cd docs-site && npm run build

[private]
docs-deps:
    #!/usr/bin/env sh
    cd docs-site && { [ -d node_modules ] || npm ci; }

[private]
dashboard-deps:
    #!/usr/bin/env sh
    cd dashboard && { [ -d node_modules ] || npm ci; }

[private]
sdk-typescript-deps:
    #!/usr/bin/env sh
    cd sdks/typescript && { [ -d node_modules ] || npm ci; }

# Start a Postgres container for tests and `serve`
postgres-up:
    #!/usr/bin/env sh
    if [ -z "$(docker ps -q -f name=^{{pg_container}}$)" ]; then
        docker rm -f {{pg_container}} >/dev/null 2>&1 || true
        docker run -d --name {{pg_container}} -p {{pg_port}}:5432 \
            -e POSTGRES_USER=workly -e POSTGRES_PASSWORD=workly -e POSTGRES_DB=workly \
            postgres:17-alpine >/dev/null
    fi
    until docker exec {{pg_container}} pg_isready -U workly -q; do sleep 0.5; done
    echo "Postgres ready: {{pg_url}}"

# Stop and remove the Postgres container
postgres-down:
    docker rm -f {{pg_container}}

# Run `workly serve` against the local Postgres container
serve: postgres-up
    WORKLY_DATABASE_URL="{{pg_url}}" WORKLY_LOG_FORMAT=text go run ./cmd/workly serve
