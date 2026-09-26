#!/bin/bash

if [ "$1" = "client" ]; then
  cd "./frontend"
  pnpm build
  cd ".."
  if [ "$GOOS" = "windows" ]; then
    FILENAME="tam-client.exe"
  else
    FILENAME="tam-client"
  fi
  go build -ldflags="-s -w" -o "./build/${FILENAME}" ./cmd/tam-client/
  if [ "$(command -v upx)" ]; then
    upx "./build/${FILENAME}"
  fi
elif [ "$1" = "server" ]; then
  if [ "$GOOS" = "windows" ]; then
    FILENAME="tam-server.exe"
  else
    FILENAME="tam-server"
  fi
  go build -ldflags="-s -w" -o "./build/${FILENAME}" ./cmd/tam-server/
  if [ "$(command -v upx)" ]; then
    upx "./build/${FILENAME}"
  fi
else
  echo "Please specify which daemon you want to build. 'client' or 'server'."
fi
