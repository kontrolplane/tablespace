VERSION 0.8
FROM golang:1.27-alpine
WORKDIR /kontrolplane

deps:
    COPY go.mod go.sum ./
    RUN go mod download

# compile builds for the machine earthly runs on, so the binary runs where it lands.
compile:
    FROM +deps
    COPY main.go .
    COPY cmd/ cmd/
    COPY pkg/ pkg/
    ARG VERSION=dev
    ARG USEROS
    ARG USERARCH
    RUN CGO_ENABLED=0 GOOS=${USEROS} GOARCH=${USERARCH} go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o build/kontrolplane/pgui .
    SAVE ARTIFACT build/kontrolplane/pgui AS LOCAL build/kontrolplane/pgui

container:
    ARG VERSION=dev
    FROM DOCKERFILE --build-arg VERSION=${VERSION} .
    ARG tag="latest"
    SAVE IMAGE ghcr.io/kontrolplane/pgui:${tag}

# postgres starts the server in docker compose and waits until it takes connections.
postgres:
    LOCALLY
    RUN docker compose up -d --wait

# seed creates the sample databases, skipping those that exist.
seed:
    LOCALLY
    WAIT
        BUILD +postgres
    END
    RUN bash seed/seed.sh

# activity starts sessions that block each other, for the activity tab.
activity:
    LOCALLY
    WAIT
        BUILD +seed
    END
    RUN bash seed/activity.sh

dev:
    LOCALLY
    WAIT
        BUILD +seed
    END
    RUN go build -o build/kontrolplane/pgui .

list:
    LOCALLY
    WAIT
        BUILD +postgres
    END
    RUN docker compose exec -T postgres psql -U postgres -c '\l'

vhs:
    LOCALLY
    WAIT
        BUILD +activity
    END
    RUN vhs vhs/cassette.tape

all:
  BUILD +compile
  BUILD +container
