# Builds one Go command from cmd/<CMD> into a distroless image.
FROM golang:1.27.2 AS build
ARG CMD
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/${CMD}

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]
