#!/bin/bash

if [ "${1}" = "client" ]; then
  cd "./frontend"
  pnpm build
  cd ".."
  go run ./cmd/tam-client/
elif [ "${1}" = "server" ]; then
  go run ./cmd/tam-server/ dev
else
  echo "Please specify which daemon you wish to run. 'client' or 'server'."
fi
