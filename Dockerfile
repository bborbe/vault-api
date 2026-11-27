FROM golang:1.27.4 AS build
WORKDIR /workspace
# Module mode, no vendoring. vendor/ is gitignored, so a bare clone has none and a
# `-mod=vendor` build fails there — which is exactly how the in-cluster release
# build clones this repo (github-release-watcher renders hack/build-job.yaml, which
# runs a plain `git fetch --depth=1` with no `go mod vendor` step). The dependency
# fetch is its own layer so a source-only change does not re-download it.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . /workspace
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s" -a -installsuffix cgo -o /main

FROM alpine:3.23
RUN apk --no-cache add \
    ca-certificates \
    git \
    gnupg \
    openssh-client \
    tini \
    tzdata \
    && rm -rf /var/cache/apk/*
ARG BUILD_GIT_VERSION=dev
ARG BUILD_GIT_COMMIT=none
ARG BUILD_DATE=unknown
COPY --from=build /main /main
ENV BUILD_GIT_VERSION=${BUILD_GIT_VERSION}
ENV BUILD_GIT_COMMIT=${BUILD_GIT_COMMIT}
ENV BUILD_DATE=${BUILD_DATE}
ENTRYPOINT ["/sbin/tini", "--", "/main"]
