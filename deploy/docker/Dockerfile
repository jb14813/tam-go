# syntax=docker/dockerfile:1
# Builds tam-server (the default) or tam-client from source and runs it from
# a minimal image. Build from the repository root:
#
#   docker build -f deploy/docker/Dockerfile -t tam-server .
#   docker build -f deploy/docker/Dockerfile --build-arg PROGRAM=tam-client -t tam-client .
#
# The data lives in /data (a volume) and the flags go on the command line,
# for example:
#
#   docker run -p 8000:8000 -v ./data:/data -e TAM_PWD=secret tam-server -addr :8000 -announce=false
#
# compose.yml next to this file does that for both programs.

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
ARG PROGRAM=tam-server
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
FROM alpine:3
ENV TAM_DATA_DIR=/data
VOLUME /data
COPY --from=build /tam /tam
ENTRYPOINT ["/tam"]
