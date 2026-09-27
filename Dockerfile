# Build stage. CGO is off because the SQLite driver is pure Go, which lets the
# final image be a static, shell-less base.
FROM golang:1.25 AS build

WORKDIR /src

# Dependencies first, so a code change does not re-download the module cache.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# The version the binary reports. The release workflow passes the version it is
# publishing; a plain `docker build` keeps "docker" so a local image cannot be
# mistaken for a release.
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/feedme ./cmd/feedme
RUN mkdir -p /out/data

# Runtime stage. distroless has no shell and no package manager, so the image
# carries only the binary and the site configs it reads.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/feedme /feedme
COPY --from=build /src/configs /configs
# The database volume must be writable by the nonroot user that runs the
# process, so the directory is owned before the VOLUME is declared.
COPY --from=build --chown=65532:65532 /out/data /data

# The database lives on a volume; the image stays disposable.
VOLUME ["/data"]

EXPOSE 8080

USER 65532:65532

ENTRYPOINT ["/feedme"]
CMD ["serve", "-addr", ":8080", "-db", "/data/feedme.db", "-site-dir", "/configs/sites"]
