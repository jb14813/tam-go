# syntax=docker/dockerfile:1
# Builds tam-server (the default) or tam-client from source and runs it from
# a minimal image. Build from the repository root:
#
#   docker build -f deploy/docker/Dockerfile -t tam-server .
#   docker build -f deploy/docker/Dockerfile --build-arg PROGRAM=tam-client -t tam-client .
#
# The data lives in /data (a volume). Inside the container both programs
# listen on every address, the server on port 8000 and the client on 3080;
# which of them the host shows, and to whom, is up to the published port:
#
#   docker run -p 8000:8000 -v ./data:/data -e TAM_PWD=secret tam-server
#   docker run -p 127.0.0.1:3080:3080 -v ./client-data:/data tam-client
#
# Flags on the command line replace the defaults, for example
# `docker run ... tam-server -addr :8000 -announce=false`. compose.yml next
# to this file runs both programs.

# The program to build: tam-server or tam-client.
ARG PROGRAM=tam-server

# The web app, embedded into tam-client. tam-server does not serve it, but
# the client package embeds the folder, so it is built for both programs.
FROM node:24-alpine AS web
RUN npm install -g pnpm@12
WORKDIR /src/frontend
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml frontend/.npmrc ./
RUN pnpm install --frozen-lockfile
COPY frontend/ ./
RUN pnpm build

FROM golang:1.27-alpine AS build
ARG PROGRAM
ARG VERSION=0.0.1
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY --from=web /src/cmd/tam-client/dist/ ./cmd/tam-client/dist/
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X ticket-auction-manager/tam-go/internal/version.Version=${VERSION}" \
      -o /tam "./cmd/${PROGRAM}"

# The program is static, so the final image only needs somewhere to keep
# the data. It runs as root, like the containers of the original, so a bind
# mount such as ./data:/data needs no ownership changes.
FROM alpine:3 AS run
ENV TAM_DATA_DIR=/data
VOLUME /data
COPY --from=build /tam /tam
ENTRYPOINT ["/tam"]

# The default flags of each program. The server's own defaults already
# listen on every address. The client's would listen on localhost, which
# inside a container no published port reaches, and try to open a browser.
FROM run AS run-tam-server
FROM run AS run-tam-client
CMD ["-addr", ":3080", "-open=false", "-tray=false"]

FROM run-${PROGRAM}
