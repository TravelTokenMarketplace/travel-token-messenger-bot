FROM golang:1.25.10-alpine AS build-stage
RUN apk update && apk upgrade && apk add build-base

WORKDIR /travel-token-messenger-bot

# add ext library
RUN apk add olm-dev

# build
COPY . .
RUN apk --no-cache add git bash grep curl jq
RUN if git rev-parse --git-dir >/dev/null 2>&1; then git submodule update --init; fi

ARG TTM_BOT_COMMIT
ARG TTM_BOT_TAG
ENV TTM_BOT_COMMIT=$TTM_BOT_COMMIT
ENV TTM_BOT_TAG=$TTM_BOT_TAG

RUN TTMBOT_PATH=$(pwd) bash ./scripts/build.sh


#runtime stage
FROM alpine:3.21 AS runtime-stage

RUN apk add --no-cache olm-dev

WORKDIR /

COPY --from=build-stage /travel-token-messenger-bot/build/travel-token-messenger-bot /travel-token-messenger-bot

ENTRYPOINT ["./travel-token-messenger-bot"]
