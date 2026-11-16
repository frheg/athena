# All Go services, one target per image:
#   docker build --target core -t athena-core .
# Targets: core, mcp-web, mcp-sources, mcp-everyday, mcp-notes, mcp-cluster-read, mcp-cluster-act, memory, notify, files.
# Programs for other machines (static binaries, no image): docker build --target clients -o <dir> . (scripts/clients)
FROM golang:1.26.10-alpine@sha256:87c65a688975c0b1d4585d204874c45ca78354670e3a5f11368c5a492303d696 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY internal internal
ARG VERSION=dev
RUN for c in core mcp-web mcp-sources mcp-everyday mcp-notes mcp-cluster-read mcp-cluster-act memory notify files athena-speaker; do \
      CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/$c ./cmd/$c || exit 1; \
    done

# distroless static: CA certificates and time zones, no shell, runs as an unprivileged user
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS base
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD ["/app", "health"]
ENTRYPOINT ["/app"]

FROM base AS core
COPY --from=build /out/core /app
COPY policy/policy.example.yaml /etc/athena/policy.yaml

FROM base AS mcp-web
COPY --from=build /out/mcp-web /app

FROM base AS mcp-sources
COPY --from=build /out/mcp-sources /app

FROM base AS mcp-everyday
COPY --from=build /out/mcp-everyday /app

FROM base AS memory
COPY --from=build /out/memory /app

FROM base AS notify
COPY --from=build /out/notify /app

FROM base AS mcp-cluster-read
COPY --from=build /out/mcp-cluster-read /app

FROM base AS mcp-cluster-act
COPY --from=build /out/mcp-cluster-act /app

FROM base AS mcp-notes
COPY --from=build /out/mcp-notes /app

FROM base AS files
COPY --from=build /out/files /app

# the programs that run on other machines, as plain files: docker build --target clients -o <dir> .
FROM scratch AS clients
COPY --from=build /out/athena-speaker /athena-speaker
