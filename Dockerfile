ARG FIRECORE_VERSION=v1.21.2

# The build stage always runs on the native architecture of the builder and cross
# compiles, so no QEMU emulation is ever involved. GOOS/GOARCH default to the
# target platform Buildx is producing, which is what the runtime image needs, and
# can be overridden explicitly to produce a binary for a platform Docker has no
# notion of (darwin).
FROM --platform=$BUILDPLATFORM golang:1.27.3-bookworm AS build

ARG VERSION="dev"
ARG TARGETOS
ARG TARGETARCH
ARG GOOS
ARG GOARCH

WORKDIR /app

RUN apt-get update && apt-get install -y git

COPY go.mod go.sum ./
RUN go mod download

COPY . ./

RUN CGO_ENABLED=0 GOOS="${GOOS:-$TARGETOS}" GOARCH="${GOARCH:-$TARGETARCH}" \
  go build -v -ldflags "-X main.version=${VERSION}" -o /app/firetron ./cmd/firetron

# Extracted by the release workflow with `--target binary --output type=local`,
# which writes the binary alone to the destination directory.
FROM scratch AS binary

COPY --from=build /app/firetron /firetron

FROM ghcr.io/streamingfast/firehose-core:${FIRECORE_VERSION}

# Pull Ubuntu security fixes that landed after the firehose-core base image was
# built, so the image does not ship known-vulnerable OS packages (the release
# workflow gates on critical/high CVEs).
RUN apt-get update \
  && apt-get upgrade -y --no-install-recommends \
  && rm -rf /var/lib/apt/lists/*

COPY --from=build /app/firetron /app/firetron
