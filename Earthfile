VERSION 0.8
FROM golang:1.26-alpine
WORKDIR /kontrolplane

deps:
    COPY go.mod go.sum ./
    RUN go mod download

compile:
    FROM +deps
    COPY main.go .
    COPY cmd/ cmd/
    COPY pkg/ pkg/
    ARG VERSION=dev
    RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o build/kontrolplane/pgui .
    SAVE ARTIFACT build/kontrolplane/pgui AS LOCAL build/kontrolplane/pgui

container:
    ARG VERSION=dev
    FROM DOCKERFILE --build-arg VERSION=${VERSION} .
    ARG tag="latest"
    SAVE IMAGE ghcr.io/kontrolplane/pgui:${tag}

seed:
    LOCALLY
    RUN bash seed/seed.sh

activity:
    LOCALLY
    RUN bash seed/activity.sh

postgres:
    LOCALLY
    RUN docker compose up -d --wait

dev:
    LOCALLY
    WAIT
        BUILD +postgres
    END
    RUN bash seed/seed.sh
    RUN go build -o build/kontrolplane/pgui .

list:
    LOCALLY
    RUN docker compose exec -T postgres psql -U postgres -c '\l'

vhs:
    LOCALLY
    RUN bash seed/activity.sh
    RUN vhs vhs/cassette.tape

all:
  BUILD +compile
  BUILD +container
